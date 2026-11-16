package meter

import (
	"fmt"
	"reflect"
)

// Errorf admits formatter buffers, escaped strings, and the final retained
// error text. Formats are engine constants; dynamic padding is never used.
func Errorf(m *Meter, format string, args ...any) error {
	size := Add(256, uint64(len(format)))
	for _, value := range args {
		switch v := value.(type) {
		case string:
			size = Add(size, Add(2, Mul(6, uint64(len(v)))))
		case error:
			size = Add(size, uint64(len(v.Error())))
		default:
			typ := reflect.TypeOf(value)
			if typ != nil && typ.Kind() == reflect.String {
				size = Add(size, Add(2, Mul(6, uint64(reflect.ValueOf(value).Len()))))
			} else {
				size = Add(size, 256)
			}
		}
	}
	// A formatter grows its temporary buffer geometrically; both discarded
	// buffers and the retained final string stay admitted to document Close.
	m.Reserve(AllocationSize(Mul(4, size)))
	return fmt.Errorf(format, args...)
}
