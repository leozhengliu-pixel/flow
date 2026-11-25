package meter

// AllocationSize mirrors the Go 1.26 allocator classes and 8 KiB pages. The
// extra header is conservative for both scanning and non-scanning objects.
// This is allocation-width rounding, not a multiplier on wire input.
func AllocationSize(size uint64) uint64 {
	size = Add(size, 16)
	classes := [...]uint64{
		8, 16, 24, 32, 48, 64, 80, 96, 112, 128, 144, 160, 176, 192,
		208, 224, 240, 256, 288, 320, 352, 384, 416, 448, 480, 512,
		576, 640, 704, 768, 896, 1024, 1152, 1280, 1408, 1536,
		1792, 2048, 2304, 2688, 3072, 3200, 3456, 4096, 4864,
		5376, 6144, 6528, 6784, 6912, 8192, 9472, 9728, 10240,
		10880, 12288, 13568, 14336, 16384, 18432, 19072, 20480,
		21760, 24576, 27264, 28672, 32768,
	}
	for _, class := range classes {
		if size <= class {
			return class
		}
	}
	return Add(size, 8191) &^ 8191
}
