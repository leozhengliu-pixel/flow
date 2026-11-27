package main

import (
	"fmt"
	"testing"

	"flow/api/internal/domain"
)

func BenchmarkLegacyMentions(b *testing.B) {
	for _, count := range []int{100, 1000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			ids := make([]string, count)
			for index := range ids {
				ids[index] = fmt.Sprintf("former-%d", index)
			}
			state := mentioningCollaborationState(ids...)
			data := domain.Bootstrap{FormerMembers: func() map[string]bool {
				return map[string]bool{ids[0]: true}
			}}
			b.ReportAllocs()
			b.SetBytes(int64(len(state)))
			b.ResetTimer()
			for range b.N {
				if err := refuseFormerMemberStateMentions(&data, nil, state, state, ""); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
