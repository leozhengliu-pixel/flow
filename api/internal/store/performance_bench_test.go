package store

import "testing"

// BenchmarkBootstrapFor captures the cost of the read path used by nearly
// every initial page load. Keep this benchmark small and deterministic so it
// can be run before and after database/storage changes.
func BenchmarkBootstrapFor(b *testing.B) {
	repository, err := OpenSQLite(b.TempDir() + "/flow.db")
	if err != nil {
		b.Fatal(err)
	}
	defer repository.Close()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok := repository.BootstrapFor(""); !ok {
			b.Fatal("workspace not found")
		}
	}
}
