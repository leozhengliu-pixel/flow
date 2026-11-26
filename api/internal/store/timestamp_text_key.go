package store

import (
	"database/sql/driver"

	"modernc.org/sqlite"
)

func init() {
	sqlite.MustRegisterDeterministicScalarFunction("flow_timestamp_key", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		if value, ok := args[0].(string); ok {
			return normalizeStoredTime(value), nil
		}
		return args[0], nil
	})
}

// timestampTextKey compares timestamps extracted from JSON without changing
// the payload. Convert only the whole second with native date arithmetic:
// the fractional digits must retain nanosecond precision.
func (s *SQLiteStore) timestampTextKey(value string) string {
	if s.dialect == "sqlite" {
		return "flow_timestamp_key(" + value + ")"
	}
	second, zone := "LEFT("+value+",19)", "RIGHT("+value+",6)"
	wholeUTC := "TO_CHAR(((" + second + " || " + zone + ")::timestamptz AT TIME ZONE 'UTC'),'YYYY-MM-DD\"T\"HH24:MI:SS')"
	offset := value + ` ~ '[+-][0-9]{2}:[0-9]{2}$'`
	if s.dialect == "mysql" {
		minutes := "(60*CAST(SUBSTR(" + zone + ",2,2) AS SIGNED)+CAST(RIGHT(" + zone + ",2) AS SIGNED))"
		shift := "CASE WHEN LEFT(" + zone + ",1)='+' THEN -" + minutes + " ELSE " + minutes + " END"
		wholeUTC = "DATE_FORMAT(TIMESTAMPADD(MINUTE," + shift + ",CAST(REPLACE(" + second + ",'T',' ') AS DATETIME)),'%Y-%m-%dT%H:%i:%s')"
		offset = "REGEXP_LIKE(" + value + `,'[+-][0-9]{2}:[0-9]{2}$')`
	}
	key := func(whole string, overhead string) string {
		fraction := "SUBSTR(" + value + ",21,GREATEST(CHAR_LENGTH(" + value + ")-" + overhead + ",0))"
		return "CONCAT(" + whole + ",'.',RPAD(" + fraction + ",9,'0'),'Z')"
	}
	// Go emits the UTC form for most rows. Only offsets need date arithmetic.
	return "(CASE WHEN SUBSTR(" + value + ",11,1)<>'T' THEN " + value +
		" WHEN RIGHT(" + value + ",1)='Z' THEN " + key(second, "21") +
		" WHEN " + offset + " THEN " + key(wholeUTC, "26") +
		" ELSE " + value + " END)"
}
