package store

func recordJournalContentKeySQL(dialect, resource, id string) string {
	key := "json_array(" + resource + "," + id + ")"
	character := func(n string) string { return "char(" + n + ")" }
	escape := func(value string) string { return "(char(92)||'" + value + "')" }
	if dialect == "postgres" {
		key = "('['||to_json(" + resource + ")::text||','||to_json(" + id + ")::text||']')"
		character = func(n string) string { return "chr(" + n + ")" }
		escape = func(value string) string { return "(chr(92)||'" + value + "')" }
	} else if dialect == "mysql" {
		key = "CONCAT('[',JSON_QUOTE(" + resource + "),',',JSON_QUOTE(" + id + "),']')"
		character = func(n string) string {
			if n == "8232" {
				return "CONVERT(0xE280A8 USING utf8mb4)"
			}
			return "CONVERT(0xE280A9 USING utf8mb4)"
		}
		escape = func(value string) string { return "CONCAT(CHAR(92),'" + value + "')" }
	}
	// encoding/json's canonical pair also escapes HTML and line separators.
	// SQL JSON constructors differ here even when their decoded values agree.
	for _, pair := range [][2]string{{"'<'", "u003c"}, {"'>'", "u003e"}, {"'&'", "u0026"}, {character("8232"), "u2028"}, {character("8233"), "u2029"}} {
		key = "REPLACE(" + key + "," + pair[0] + "," + escape(pair[1]) + ")"
	}
	return key
}
