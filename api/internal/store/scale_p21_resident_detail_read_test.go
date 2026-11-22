package store

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"sync"
	"testing"

	"flow/api/internal/domain"
)

func TestResidentDetailReaders(t *testing.T) {
	f, view, workspace := coldIncrementalFixture(t)
	ctx := t.Context()
	const documentID = "ci-doc-1"
	if err := f.MutateWorkspace(ctx, workspace, "scale_p21.resident_detail_seed", documentID, nil, func(data *domain.Bootstrap) error {
		i := slices.IndexFunc(data.Documents, func(doc domain.Document) bool { return doc.ID == documentID })
		data.Documents[i].ProjectIDs = []string{"ci-project-05"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := view.ReloadWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	request := DocumentReadRequest{Ref: documentID, ReadOnly: true, ProjectionFallback: true}
	read, err := view.BeginDocumentRead(ctx, workspace, "", nil, request)
	if err != nil {
		t.Fatal(err)
	}
	want, err := json.Marshal(read.Data)
	read.Close()
	if err != nil {
		t.Fatal(err)
	}
	source, err := view.retainMetadataLogicalSource(ctx, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	image, ok, err := view.metadataResidency.residentImage(ctx, workspace, source)
	if err != nil || !ok {
		t.Fatalf("failed to warm image: %v", err)
	}
	var readers sync.WaitGroup
	problems := make(chan error, 8)
	for range cap(problems) {
		readers.Go(func() {
			if err := checkResidentDetail(ctx, view, workspace, image, request, want); err != nil {
				problems <- err
			}
		})
	}
	readers.Wait()
	close(problems)
	for err := range problems {
		t.Error(err)
	}
	index := slices.IndexFunc(image.data.Documents, func(doc domain.Document) bool { return doc.ID == documentID })
	if got := image.data.Documents[index].ProjectIDs[0]; got != "ci-project-05" {
		t.Errorf("reader modified the shared document's project: %s", got)
	}
	if used := view.MetadataWorkUsedForTesting(); used != 0 {
		t.Fatalf("readers retained %d work bytes", used)
	}
}

func checkResidentDetail(ctx context.Context, view *SQLiteStore, workspace string, image *coldDecodedImage, request DocumentReadRequest, want []byte) error {
	read, err := view.BeginDocumentRead(ctx, workspace, "", nil, request)
	if err != nil {
		return err
	}
	defer read.Close()
	if read.owner.image != image || !read.owner.residentField("documents").IsValid() {
		return fmt.Errorf("detail missed the resident image")
	}
	got, err := json.Marshal(read.Data)
	if err != nil || !slices.Equal(got, want) {
		return fmt.Errorf("resident detail changed the wire output: %v", err)
	}
	read.Data.Documents[0].ProjectIDs[0] = "owned-output"
	var projects []string
	err = read.owner.visitNativeID(ctx, "projects", "ci-project-05", func(i int, _ metadataLogicalNode) error {
		projects = append(projects, image.data.Projects[i].ID)
		return nil
	})
	if err != nil || !reflect.DeepEqual(projects, []string{"ci-project-05"}) {
		return fmt.Errorf("ID index selected %v: %v", projects, err)
	}
	return nil
}
