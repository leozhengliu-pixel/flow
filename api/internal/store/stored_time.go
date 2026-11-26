package store

import "time"

// storedTimeLayout is for SQL timestamp columns. RFC3339Nano drops trailing
// zeros, so "05.1Z" sorts after "05.12Z" and "05Z" after both. Fixed-width UTC
// makes text order chronological without converting indexed columns at read
// time. JSON payloads still use their own wire format.
const storedTimeLayout = "2006-01-02T15:04:05.000000000Z"

// storedTimeSecond is the length of a stored time's text up to whole seconds.
// Every text form of a UTC time in one second starts with the same 19 bytes,
// so rows of one second are adjacent in text order and only their order
// within the second can be wrong.
const storedTimeSecond = len("2006-01-02T15:04:05")

func formatStoredTime(at time.Time) string {
	return at.UTC().Format(storedTimeLayout)
}

// normalizeStoredTime is raw (RFC3339 with any fraction) in storedTimeLayout;
// text that is not a time is returned unchanged.
func normalizeStoredTime(raw string) string {
	at, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return raw
	}
	return formatStoredTime(at)
}
