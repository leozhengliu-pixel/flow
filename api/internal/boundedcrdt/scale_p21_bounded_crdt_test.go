package boundedcrdt

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/rand"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"flow/api/internal/boundedcrdt/internal/doc"
	"flow/api/internal/boundedcrdt/internal/lib0"

	"github.com/Deln0r/ygo"
)

var scaleP21CRDTBudget = errors.New("test CRDT work exhausted")

type scaleP21CRDTAdmission struct {
	used, peak uint64
	limit      uint64
	calls      int
	failAt     int
	cancelAt   int
	cancel     context.CancelFunc
}

func (a *scaleP21CRDTAdmission) acquire(size uint64) (func(), error) {
	a.calls++
	if a.failAt != 0 && a.calls >= a.failAt {
		return nil, scaleP21CRDTBudget
	}
	if a.limit != 0 && size > a.limit-a.used {
		return nil, scaleP21CRDTBudget
	}
	a.used += size
	a.peak = max(a.peak, a.used)
	if a.cancelAt != 0 && a.calls == a.cancelAt {
		a.cancel()
	}
	done := false
	return func() {
		if !done {
			done = true
			a.used -= size
		}
	}, nil
}

func scaleP21CRDTTree(d *ygo.Doc) map[string]any {
	var children func(interface{ Range(func(uint64, any) bool) }) []any
	children = func(parent interface{ Range(func(uint64, any) bool) }) []any {
		out := []any{}
		parent.Range(func(_ uint64, child any) bool {
			if el, ok := child.(*ygo.XmlElement); ok {
				attrs := map[string]any{}
				for key, value := range el.Attributes() {
					attrs[key] = value
				}
				out = append(out, map[string]any{"type": el.NodeName(), "attrs": attrs, "content": children(el)})
			}
			return true
		})
		return out
	}
	return map[string]any{"type": "doc", "content": children(ygo.NewXmlFragment(d, "prosemirror"))}
}

func scaleP21CRDTUpdates(t *testing.T, large bool) [][]byte {
	t.Helper()
	d := ygo.NewDocWithOptions(ygo.Options{ClientID: 31})
	updates := [][]byte{}
	stop := d.OnUpdate(func(update []byte, _ any) { updates = append(updates, append([]byte(nil), update...)) })
	defer stop()
	fragment := ygo.NewXmlFragment(d, "prosemirror")
	tx := d.WriteTxn()
	p := fragment.InsertXmlElement(tx, 0, "paragraph")
	mention := p.InsertXmlElement(tx, 0, "mention")
	mention.SetAttribute(tx, "id", "retained")
	mention.SetAttribute(tx, "label", "person")
	text := p.InsertXmlText(tx, 1)
	if err := text.Insert(tx, 0, "hello \u4e16\u754c \U0001f600"); err != nil {
		t.Fatal(err)
	}
	tx.Commit()
	tx = d.WriteTxn()
	mention.RemoveAttribute(tx, "label")
	mention.SetAttribute(tx, "id", "new-former")
	if large {
		mention.SetAttribute(tx, "label", strings.Repeat("legal-large-value", 24<<10))
	}
	if err := text.Format(tx, 0, 5, map[string]any{"bold": true, "link": map[string]any{"href": "https://example.invalid", "title": []any{"x", 2.0}}}); err != nil {
		t.Fatal(err)
	}
	tx.Commit()
	tx = d.WriteTxn()
	if err := text.Delete(tx, 1, 2); err != nil {
		t.Fatal(err)
	}
	nested := p.InsertXmlElement(tx, 2, "paragraph")
	nestedMention := nested.InsertXmlElement(tx, 0, "mention")
	nestedMention.SetAttribute(tx, "id", "nested")
	tx.Commit()
	return updates
}

func TestScaleP21BoundedCRDTProducerDifferential(t *testing.T) {
	for _, large := range []bool{false, true} {
		updates := scaleP21CRDTUpdates(t, large)
		for _, mode := range []string{"warm", "reloaded", "out-of-order"} {
			t.Run(mode+map[bool]string{false: "", true: "/large"}[large], func(t *testing.T) {
				old := ygo.NewDocWithOptions(ygo.Options{ClientID: 32})
				a := &scaleP21CRDTAdmission{}
				d, err := NewDoc(t.Context(), a.acquire)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					d.Close()
					if a.used != 0 {
						t.Fatal("admission leaked", a.used)
					}
				}()
				inputs := append([][]byte(nil), updates...)
				switch mode {
				case "reloaded":
					whole := ygo.NewDoc()
					for _, update := range inputs {
						if err := ygo.ApplyUpdate(whole, update); err != nil {
							t.Fatal(err)
						}
					}
					inputs = [][]byte{ygo.EncodeStateAsUpdate(whole)}
				case "out-of-order":
					for i, j := 0, len(inputs)-1; i < j; i, j = i+1, j-1 {
						inputs[i], inputs[j] = inputs[j], inputs[i]
					}
				}
				for i, update := range inputs {
					if err := ygo.ApplyUpdate(old, update); err != nil {
						t.Fatal(err)
					}
					if err := d.ApplyState(base64.StdEncoding.EncodeToString(update)); err != nil {
						t.Fatalf("apply %d: %v", i, err)
					}
					got, err := d.MentionDocument()
					if err != nil {
						t.Fatal(err)
					}
					if want := scaleP21CRDTTree(old); !reflect.DeepEqual(got, want) {
						t.Fatalf("tree differs at update %d", i)
					}
				}
				t.Logf("updates=%d admission_calls=%d peak_work=%d", len(inputs), a.calls, a.peak)
			})
		}
	}
}

func TestScaleP21BoundedCRDTAdmissionUnwind(t *testing.T) {
	updates := scaleP21CRDTUpdates(t, true)
	run := func(t *testing.T, a *scaleP21CRDTAdmission, ctx context.Context) error {
		t.Helper()
		d, err := NewDoc(ctx, a.acquire)
		if err != nil {
			if a.used != 0 {
				t.Fatal("construction leaked", a.used)
			}
			return err
		}
		engine := d.engine
		for _, update := range updates {
			if err = d.ApplyUpdate(update); err != nil {
				break
			}
		}
		if err == nil {
			_, err = d.MentionDocument()
		}
		d.Close()
		d.Close()
		if a.used != 0 {
			t.Fatal("owner leaked", a.used)
		}
		done := make(chan struct{})
		go func() {
			engine.Branch(nil, "unwind-proof")
			stop := engine.OnAfterTransaction(nil, func(_ *doc.TransactionMut) {})
			stop()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("engine lock remained held")
		}
		return err
	}
	baseline := &scaleP21CRDTAdmission{}
	if err := run(t, baseline, t.Context()); err != nil {
		t.Fatal(err)
	}
	for ordinal := 1; ordinal <= baseline.calls; ordinal++ {
		a := &scaleP21CRDTAdmission{failAt: ordinal}
		if err := run(t, a, t.Context()); !errors.Is(err, scaleP21CRDTBudget) {
			t.Fatalf("failure %d: %v", ordinal, err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		a = &scaleP21CRDTAdmission{cancelAt: ordinal, cancel: cancel}
		err := run(t, a, ctx)
		cancel()
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation %d: %v", ordinal, err)
		}
	}
	t.Logf("proved every acquisition ordinal 1..%d", baseline.calls)
}

func TestScaleP21BoundedCRDTMalformed(t *testing.T) {
	for _, raw := range [][]byte{nil, {}, {0}, {1, 255, 255, 255, 255, 127}, {1, 1, 1, 0, 255}, {0, 1, 1, 255}} {
		a := &scaleP21CRDTAdmission{}
		d, err := NewDoc(t.Context(), a.acquire)
		if err != nil {
			t.Fatal(err)
		}
		if err := d.ApplyUpdate(raw); !errors.Is(err, ErrInvalidUpdate) {
			t.Fatalf("%x: %v", raw, err)
		}
		d.Close()
		if a.used != 0 {
			t.Fatal("malformed update leaked")
		}
	}
}

func TestScaleP21BoundedCRDTStructuralAdmission(t *testing.T) {
	// Scalar clock ranges are not node/expansion counts.
	for _, kind := range []byte{0, 10, 1} {
		raw := []byte{1, 1, 1, 0, kind}
		if kind == 1 {
			raw = append(raw, 1)
			raw = lib0.WriteVarString(nil, raw, "prosemirror")
		}
		raw = lib0.WriteVarUint(nil, raw, 1<<52)
		raw = append(raw, 0)
		a := &scaleP21CRDTAdmission{limit: 1 << 20}
		d, err := NewDoc(t.Context(), a.acquire)
		if err != nil {
			t.Fatal(err)
		}
		old := ygo.NewDoc()
		if err := ygo.ApplyUpdate(old, raw); err != nil {
			t.Fatal("legacy scalar range", kind, err)
		}
		if err := d.ApplyUpdate(raw); err != nil {
			t.Fatal("bounded scalar range", kind, err)
		}
		got, err := d.MentionDocument()
		if err != nil || !reflect.DeepEqual(got, scaleP21CRDTTree(old)) {
			t.Fatal("scalar range tree differs", kind, err)
		}
		d.Close()
		if a.used != 0 {
			t.Fatal("scalar range leaked")
		}
	}
	// Each nested malformed Any array attempts its advertised capacity before
	// it can discover the eventual truncation. Admit those attempted
	// capacities before allocation, rather than counting successful leaves.
	raw := []byte{1, 1, 1, 0, 8, 1}
	raw = lib0.WriteVarString(nil, raw, "prosemirror")
	raw = append(raw, 1)
	for i := 0; i < 400; i++ {
		raw = append(raw, 117)
		raw = lib0.WriteVarUint(nil, raw, 400)
	}
	for i := 0; i < 4000; i++ {
		raw = append(raw, 126)
	}
	a := &scaleP21CRDTAdmission{limit: 256 << 10}
	d, err := NewDoc(t.Context(), a.acquire)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.ApplyUpdate(raw); !errors.Is(err, scaleP21CRDTBudget) {
		t.Fatal("attempted capacities were not refused by admission", err)
	}
	d.Close()
	if a.used != 0 || a.peak > a.limit {
		t.Fatal("structural refusal leaked or exceeded allowance", a.used, a.peak)
	}
}

func TestScaleP21BoundedCRDTTrailingAndBase64(t *testing.T) {
	update := scaleP21CRDTUpdates(t, false)[0]
	update = append(append([]byte(nil), update...), 255, 0, 255)
	a := &scaleP21CRDTAdmission{}
	d, err := NewDoc(t.Context(), a.acquire)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.ApplyState("!not-base64!"); !errors.Is(err, ErrInvalidState) || !errors.Is(err, ErrInvalidUpdate) {
		t.Fatal("invalid base64 distinction", err)
	}
	old := ygo.NewDoc()
	if err := ygo.ApplyUpdate(old, update); err != nil {
		t.Fatal(err)
	}
	if err := d.ApplyUpdate(update); err != nil {
		t.Fatal(err)
	}
	got, err := d.MentionDocument()
	if err != nil || !reflect.DeepEqual(got, scaleP21CRDTTree(old)) {
		t.Fatal("legacy trailing-byte acceptance changed", err)
	}
}

func TestScaleP21BoundedCRDTClosedOwner(t *testing.T) {
	a := &scaleP21CRDTAdmission{}
	d, err := NewDoc(t.Context(), a.acquire)
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
	if d.ctx != nil || d.admit != nil || d.engine != nil || a.used != 0 {
		t.Fatal("closed owner retained request state")
	}
	for _, state := range []string{"", "invalid"} {
		if err := d.ApplyState(state); !errors.Is(err, ErrClosed) {
			t.Fatal("closed state application", err)
		}
	}
	if err := d.ApplyUpdate(nil); !errors.Is(err, ErrClosed) {
		t.Fatal("closed update application", err)
	}
	if _, err := d.MentionDocument(); !errors.Is(err, ErrClosed) {
		t.Fatal("closed tree extraction", err)
	}
}

func TestScaleP21BoundedCRDTQueuedOverwriteAdmission(t *testing.T) {
	producer := ygo.NewDocWithOptions(ygo.Options{ClientID: 193})
	var updates [][]byte
	stop := producer.OnUpdate(func(raw []byte, _ any) {
		updates = append(updates, append([]byte(nil), raw...))
	})
	defer stop()
	fragment := ygo.NewXmlFragment(producer, "prosemirror")
	tx := producer.WriteTxn()
	mention := fragment.InsertXmlElement(tx, 0, "mention")
	mention.SetAttribute(tx, "id", "initial")
	tx.Commit()
	for i := 0; i < 10000; i++ {
		tx := producer.WriteTxn()
		mention.SetAttribute(tx, "id", "person-"+strconv.Itoa(i))
		tx.Commit()
	}
	for _, count := range []int{1000, 10000} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			a := &scaleP21CRDTAdmission{limit: 256 << 20}
			d, err := NewDoc(t.Context(), a.acquire)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				d.Close()
				if a.used != 0 {
					t.Fatal("queued update owner leaked", a.used)
				}
			}()
			old := ygo.NewDoc()
			for i, update := range updates[:count+1] {
				if err := ygo.ApplyUpdate(old, update); err != nil {
					t.Fatal(err)
				}
				if err := d.ApplyUpdate(update); err != nil {
					t.Fatalf("legal queued update %d/%d refused at peak=%d: %v", i, count, a.peak, err)
				}
			}
			got, err := d.MentionDocument()
			if err != nil || !reflect.DeepEqual(got, scaleP21CRDTTree(old)) {
				t.Fatal("queued overwrite tree differs", err)
			}
			t.Logf("queued=%d admitted_peak=%d", count, a.peak)
		})
	}
}

func TestScaleP21BoundedCRDTJSONDifferential(t *testing.T) {
	cases := [][]byte{
		[]byte(`null`), []byte(`[]`), []byte(`[{}]`), []byte(`{"a":1}`),
		[]byte(`{"same":{"a":1},"same":{"b":2}}`), []byte(`"\ud800"`),
		[]byte(`-0`), []byte(`1e1000`), []byte(`{"a":}`), []byte(`[1,]`),
		[]byte(`true false`), []byte(`"unterminated`), {'"', 0xff, '"'},
		[]byte(strings.Repeat("[", 10000) + "0" + strings.Repeat("]", 10000)),
		[]byte(strings.Repeat("[", 10001) + "0" + strings.Repeat("]", 10001)),
	}
	random := rand.New(rand.NewSource(21))
	for i := 0; i < 200; i++ {
		v := map[string]any{"id": random.Intn(100), "attrs": []any{nil, true, random.Float64(), strings.Repeat("a\\\"\n", random.Intn(40))}}
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		cases = append(cases, raw)
	}
	for i, raw := range cases {
		var want any
		wantErr := json.Unmarshal(raw, &want)
		a := &scaleP21CRDTAdmission{}
		got, release, err := DecodeJSON(t.Context(), a.acquire, raw)
		if (err == nil) != (wantErr == nil) {
			t.Fatalf("case %d validity: %v vs %v", i, err, wantErr)
		}
		if err == nil && !reflect.DeepEqual(got, want) {
			t.Fatalf("case %d value mismatch", i)
		}
		if release != nil {
			release()
		}
		if a.used != 0 {
			t.Fatal("JSON owner leaked", i, a.used)
		}
	}
}
