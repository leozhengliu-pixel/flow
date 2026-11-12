package store

// Encoded bytes alone do not bound decoded maps/interfaces or their owned
// snapshot copies. Reserve conservative space for those containers before
// json.Unmarshal; complex records use the streaming primitive instead.
type metadataHydrationBudget struct {
	bytes int
	work  int
}

func newMetadataHydrationBudget() metadataHydrationBudget {
	return metadataHydrationBudget{bytes: MetadataRecordPageMaxBytes, work: 2<<20 - 64<<10}
}

func (b *metadataHydrationBudget) consume(raw []byte) error {
	if len(raw) > b.bytes {
		return ErrMetadataPageBudget
	}
	cost := len(raw) * 8
	quoted, escaped := false, false
	for _, c := range raw {
		if quoted {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				quoted = false
			}
			continue
		}
		switch c {
		case '"':
			quoted = true
			cost += 128
		case '{', '[':
			cost += 1024
		case ':', ',':
			cost += 128
		}
		if cost > b.work {
			return ErrMetadataPageBudget
		}
	}
	if cost > b.work {
		return ErrMetadataPageBudget
	}
	b.bytes -= len(raw)
	b.work -= cost
	return nil
}

func metadataRootFieldsBounded(raw []byte) bool {
	depth, fields := 0, 0
	quoted, escaped := false, false
	for _, c := range raw {
		if quoted {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				quoted = false
			}
			continue
		}
		switch c {
		case '"':
			quoted = true
		case '{', '[':
			depth++
		case '}', ']':
			depth--
		case ':':
			if depth == 1 {
				fields++
				if fields > 256 {
					return false
				}
			}
		}
	}
	return true
}
