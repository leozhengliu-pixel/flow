package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// MCP attachment tools: files (prepare_attachment_upload + PUT +
// create_attachment_from_upload, or create_attachment) and links
// (save_attachment) on an issue or a document. A file attached to a document
// is recorded on the document (document_attachments.go), so its /uploads URL
// is served to everyone who can see the document, as for files pasted into
// the editor.

// mcpAttachmentTarget is the record an attachment tool writes to: an issue or a document.
type mcpAttachmentTarget struct {
	issue    domain.Issue
	document domain.Document
}

func (t mcpAttachmentTarget) onDocument() bool { return t.document.ID != "" }

// purpose binds a prepared upload to its target (store.FileUpload.Purpose).
func (t mcpAttachmentTarget) purpose() string {
	if t.onDocument() {
		return "document:" + t.document.ID
	}
	return "issue:" + t.issue.ID
}

func (t mcpAttachmentTarget) noun() string {
	if t.onDocument() {
		return "document"
	}
	return "issue"
}

// writeError explains a refused document write (the role changed after the
// tool resolved the target).
func (t mcpAttachmentTarget) writeError(err error) error {
	if t.onDocument() && errors.Is(err, store.ErrAuthForbidden) {
		return mcpDocumentNotEditable(t.document)
	}
	return err
}

func mcpDocumentNotEditable(document domain.Document) error {
	return fmt.Errorf("you cannot edit document %q, so nothing can be attached to it; ask its owner for edit access", document.Title)
}

// mcpAttachmentTarget resolves the `issue` or `document` argument (exactly
// one) of an attachment tool. A document must be one the actor can edit, the
// rule of the REST document upload (including a private team's inherited
// scope); the write re-checks it.
func (s *server) mcpAttachmentTarget(data domain.Bootstrap, tool string, args map[string]any) (mcpAttachmentTarget, error) {
	issueRef, documentRef := stringArg(args, "issue"), stringArg(args, "document")
	if (issueRef == "") == (documentRef == "") {
		return mcpAttachmentTarget{}, fmt.Errorf("Invalid arguments for %s: pass exactly one of `issue` (issue ID or identifier) or `document` (document ID or slug); nothing was saved. Call it again with corrected arguments.", tool)
	}
	if documentRef == "" {
		issue, err := mcpFindIssue(data, issueRef)
		return mcpAttachmentTarget{issue: issue}, err
	}
	document, err := mcpFindDocument(data, documentRef)
	if err != nil {
		return mcpAttachmentTarget{}, err
	}
	if !canEditDocument(documentRole(s, data, document)) {
		return mcpAttachmentTarget{}, mcpDocumentNotEditable(document)
	}
	return mcpAttachmentTarget{document: document}, nil
}

// mcpAttachmentInsert reads `insert`: "append" places the file at the end of
// the body, "none" leaves the body alone. Issues default to "append" (files
// always were embedded in the description), documents to "none".
func mcpAttachmentInsert(args map[string]any, target mcpAttachmentTarget) (bool, error) {
	switch lowerArg(args, "insert") {
	case "":
		return !target.onDocument(), nil
	case "append":
		return true, nil
	case "none":
		return false, nil
	}
	return false, fmt.Errorf(`insert must be "none" or "append"`)
}

// mcpUploadContentType is the media type a document upload is stored with:
// the given one, else the filename's, else application/octet-stream.
func mcpUploadContentType(filename, contentType string) (string, error) {
	contentType = strings.TrimSpace(contentType)
	if contentType == "" {
		contentType = mime.TypeByExtension(strings.ToLower(filepath.Ext(filename)))
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if _, _, err := mime.ParseMediaType(contentType); err != nil || uploadContentTypeTooLong(contentType) {
		return "", fmt.Errorf("contentType must be a valid media type such as image/png; nothing was uploaded")
	}
	return contentType, nil
}

// MCP uploads share the durable REST upload records (files_api.go), so the
// PUT and the finalizing tool call may reach different API instances.
func (s *server) prepareMCPAttachmentUpload(ctx context.Context, actor mcpActor, data domain.Bootstrap, args map[string]any) (any, error) {
	target, err := s.mcpAttachmentTarget(data, "prepare_attachment_upload", args)
	if err != nil {
		return nil, err
	}
	size := int64(intArg(args, "size", 0))
	if size < 0 || size > maxUploadBytes {
		return nil, fmt.Errorf("Flow attachments must be 20 MB or smaller")
	}
	filename := cleanUploadFilename(stringArg(args, "filename"))
	if filename == "" {
		filename = "upload"
	}
	for _, field := range []string{"title", "subtitle"} {
		if checkUploadTitle(stringArg(args, field)) != nil {
			return nil, fmt.Errorf("%s must be %d characters or fewer; nothing was uploaded", field, domain.UploadTitleMaxLength)
		}
	}
	contentType := stringArg(args, "contentType")
	if uploadContentTypeTooLong(contentType) {
		return nil, fmt.Errorf("contentType must be a media type of at most 255 characters")
	}
	if target.onDocument() {
		if contentType, err = mcpUploadContentType(filename, contentType); err != nil {
			return nil, err
		}
	}
	upload := store.FileUpload{WorkspaceKey: actor.WorkspaceKey, UserID: actor.User.ID, Filename: filename, ContentType: contentType, ExpectedSize: size, Purpose: target.purpose(), Title: stringArg(args, "title"), Subtitle: stringArg(args, "subtitle")}
	payload, err := s.prepareFileUpload(ctx, upload, stringArg(args, "__flowBaseURL"), "/api/mcp/uploads/")
	if errors.Is(err, store.ErrFileUploadQuota) {
		return nil, err
	}
	if err != nil {
		return nil, fmt.Errorf("could not prepare the upload: %w", err)
	}
	headers := map[string]string{"Content-Type": upload.ContentType}
	// uploadUrl takes the one PUT; assetUrl (and its path, url) is where the
	// file is served once create_attachment_from_upload attached it.
	return map[string]any{"uploadUrl": payload.UploadURL, "assetUrl": payload.AssetURL, "url": "/uploads/" + uploadNameFromURL(payload.AssetURL), "headers": headers, "expiresAt": payload.ExpiresAt}, nil
}

func (s *server) cleanupMCPUploads(ctx context.Context) { s.cleanupFileUploads(ctx) }

func (s *server) putMCPUpload(w http.ResponseWriter, r *http.Request) {
	s.receiveFileUpload(w, r, r.PathValue("token"), "issue", "document")
}

// mcpCompletedUpload finds the actor's completed, unexpired upload named by
// assetUrl: the stable asset URL (…/uploads/asset_…) or, as clients sent
// before the two were distinct, the one-time upload URL.
func (s *server) mcpCompletedUpload(ctx context.Context, actor mcpActor, assetURL string) (store.FileUpload, error) {
	parsed, err := url.Parse(assetURL)
	if err != nil || assetURL == "" {
		return store.FileUpload{}, fmt.Errorf("invalid assetUrl")
	}
	var pending store.FileUpload
	if name := uploadNameFromURL(assetURL); name != "" {
		pending, err = s.store.FileUploadByObject(ctx, name)
	} else {
		pending, err = s.store.FileUpload(ctx, secretHash(filepath.Base(parsed.Path)))
	}
	if err != nil || pending.Status != store.FileUploadCompleted || !pending.ExpiresAt.After(fileUploadNow()) || pending.WorkspaceKey != actor.WorkspaceKey || pending.UserID != actor.User.ID {
		if err != nil && !errors.Is(err, store.ErrFileUploadNotFound) {
			return store.FileUpload{}, err
		}
		return store.FileUpload{}, fmt.Errorf("assetUrl is not a completed Flow upload")
	}
	return pending, nil
}

func (s *server) finalizeMCPAttachmentUpload(ctx context.Context, actor mcpActor, data domain.Bootstrap, args map[string]any) (any, error) {
	target, err := s.mcpAttachmentTarget(data, "create_attachment_from_upload", args)
	if err != nil {
		return nil, err
	}
	insert, err := mcpAttachmentInsert(args, target)
	if err != nil {
		return nil, err
	}
	pending, err := s.mcpCompletedUpload(ctx, actor, stringArg(args, "assetUrl"))
	if err != nil {
		return nil, err
	}
	if pending.Purpose != target.purpose() {
		return nil, fmt.Errorf("upload was prepared for a different %s", target.noun())
	}
	claimed, err := s.store.ClaimCompletedFileUpload(ctx, pending.ID, fileUploadNow())
	if err != nil {
		return nil, err
	}
	if !claimed {
		return nil, fmt.Errorf("assetUrl is not a completed Flow upload")
	}
	title := firstNonEmpty(stringArg(args, "title"), pending.Title, pending.Filename)
	attachment := domain.Attachment{Title: title, Subtitle: firstNonEmpty(stringArg(args, "subtitle"), pending.Subtitle), URL: "/uploads/" + pending.ObjectKey, ContentType: pending.ContentType, Size: pending.ActualSize}
	result, err := s.attachMCPFile(ctx, actor, target, attachment, insert, args)
	if err != nil {
		_ = s.store.ReleaseFileUpload(context.WithoutCancel(ctx), pending.ID, fileUploadNow())
		return nil, err
	}
	_ = s.store.DeleteFileUpload(context.WithoutCancel(ctx), pending.ID)
	return result, nil
}

func (s *server) createMCPAttachment(ctx context.Context, actor mcpActor, data domain.Bootstrap, args map[string]any) (any, error) {
	target, err := s.mcpAttachmentTarget(data, "create_attachment", args)
	if err != nil {
		return nil, err
	}
	insert, err := mcpAttachmentInsert(args, target)
	if err != nil {
		return nil, err
	}
	content, err := base64.StdEncoding.DecodeString(stringArg(args, "base64Content"))
	if err != nil {
		return nil, fmt.Errorf("base64Content is invalid")
	}
	digest := sha256.Sum256(content)
	if !strings.EqualFold(hex.EncodeToString(digest[:]), stringArg(args, "sha256")) {
		return nil, fmt.Errorf("sha256 checksum mismatch")
	}
	if expected := intArg(args, "size", 0); hasNumberArg(args, "size") && expected != len(content) {
		return nil, fmt.Errorf("attachment size mismatch")
	}
	if len(content) > maxUploadBytes {
		return nil, fmt.Errorf("Flow attachments must be 20 MB or smaller")
	}
	filename, contentType := stringArg(args, "filename"), stringArg(args, "contentType")
	key := fmt.Sprintf("mcp_%s_%s", idgen.Stamp(), filepath.Base(filename))
	if target.onDocument() {
		// Document files get the unguessable, Markdown-safe names of REST uploads.
		if contentType, err = mcpUploadContentType(cleanUploadFilename(filename), contentType); err != nil {
			return nil, err
		}
		if key, err = assetObjectKey(filename); err != nil {
			return nil, err
		}
	}
	storage, err := s.storage()
	if err != nil {
		return nil, err
	}
	upload, policyErr := s.checkUploadPolicy(actor.WorkspaceKey, filename, bytes.NewReader(content))
	if policyErr != nil {
		return nil, policyErr
	}
	size, err := storage.Put(ctx, key, upload, contentType)
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = storage.Delete(cleanup, key)
		return nil, err
	}
	title := firstNonEmpty(stringArg(args, "title"), filepath.Base(filename))
	attachment := domain.Attachment{Title: title, Subtitle: stringArg(args, "subtitle"), URL: "/uploads/" + key, ContentType: contentType, Size: size}
	result, err := s.attachMCPFile(ctx, actor, target, attachment, insert, args)
	if err != nil {
		_ = storage.Delete(context.WithoutCancel(ctx), key)
	}
	return result, err
}

// attachMCPFile records a stored file on its target. On an issue, insert
// embeds it in the description in the same write (as always); on a document
// it is appended to the body afterwards, like a save_document append patch.
func (s *server) attachMCPFile(ctx context.Context, actor mcpActor, target mcpAttachmentTarget, attachment domain.Attachment, insert bool, args map[string]any) (map[string]any, error) {
	attachment.ID, attachment.CreatedAt, attachment.Creator, attachment.BotActor = idgen.New("attachment_"), time.Now().UTC(), actor.User, actor.User.Via
	if !target.onDocument() {
		saved, err := s.attachStoredObject(ctx, actor, target.issue.ID, attachment, insert)
		if err != nil {
			return nil, err
		}
		return mcpAttachmentResult(args, saved, target, insert), nil
	}
	saved, err := s.recordDocumentAttachment(ctx, actor.WorkspaceKey, target.document.ID, attachment)
	if err != nil {
		return nil, target.writeError(err)
	}
	result := mcpAttachmentResult(args, saved, target, false)
	if insert {
		if err := s.appendMCPDocumentEmbed(ctx, actor, target.document.ID, saved, args); err != nil {
			result["message"] = "The file is attached to the document, but it could not be added to the body (" + err.Error() + "). Place `markdown` in the document with save_document."
		} else {
			result["inserted"] = true
		}
	}
	return result, nil
}

func (s *server) attachStoredObject(ctx context.Context, actor mcpActor, issueID string, attachment domain.Attachment, embed bool) (domain.Attachment, error) {
	ctx = store.WithIssueRecordMutations(ctx, issueID)
	attachment.IssueID = issueID
	err := s.store.MutateWorkspace(ctx, actor.WorkspaceKey, "attachment.created", issueID, nil, func(data *domain.Bootstrap) error {
		issue, err := issueByID(data, issueID)
		if err != nil {
			return err
		}
		issue.Attachments = append(issue.Attachments, attachment)
		if !embed {
			return nil
		}
		return applyIssueAttachmentEmbed(data, issue, attachment)
	})
	return attachment, err
}

// appendMCPDocumentEmbed adds the file as an image, video or file card block
// at the end of the document body, through save_document's append patch
// (version-checked, Markdown and editor document kept in step).
func (s *server) appendMCPDocumentEmbed(ctx context.Context, actor mcpActor, documentID string, attachment domain.Attachment, args map[string]any) error {
	block := attachmentEmbedMarkdown(attachment)
	if block == "" {
		return nil
	}
	data, err := s.mcpWorkspaceData(ctx, actor)
	if err != nil {
		return err
	}
	document, err := mcpFindDocument(data, documentID)
	if err != nil {
		return err
	}
	if strings.Contains(document.Content, attachment.URL) {
		return nil
	}
	text := "\n\n" + block + "\n"
	switch {
	case strings.TrimSpace(document.Content) == "", strings.HasSuffix(document.Content, "\n\n"):
		text = block + "\n"
	case strings.HasSuffix(document.Content, "\n"):
		text = "\n" + block + "\n"
	}
	_, err = s.saveMCPDocument(ctx, actor, data, map[string]any{"id": document.ID, "patch": []any{map[string]any{"op": "append", "text": text}}, "__flowBaseURL": args["__flowBaseURL"]})
	return err
}

// mcpAttachmentResult is an attachment tool's receipt: the attachment, the
// file's absolute assetUrl, whether it was placed in the body, and a Markdown
// snippet (![title](url) for images, [title](url) otherwise) the caller can
// put in the body itself.
func mcpAttachmentResult(args map[string]any, attachment domain.Attachment, target mcpAttachmentTarget, inserted bool) map[string]any {
	result := map[string]any{}
	_ = jsonClone(attachment, &result)
	if target.onDocument() {
		delete(result, "issueId")
		result["documentId"] = target.document.ID
	}
	if strings.HasPrefix(attachment.URL, "/uploads/") {
		result["assetUrl"] = strings.TrimRight(stringArg(args, "__flowBaseURL"), "/") + attachment.URL
	}
	result["markdown"] = attachmentMarkdownLink(attachment)
	result["inserted"] = inserted
	return result
}

// attachmentMarkdownLink is an attachment as a Markdown image (images) or
// link (everything else).
func attachmentMarkdownLink(attachment domain.Attachment) string {
	destination := strings.NewReplacer(" ", "%20", "(", "%28", ")", "%29", "<", "%3C", ">", "%3E").Replace(attachment.URL)
	link := "[" + attachmentEmbedLabel(attachment.Title) + "](" + destination + ")"
	if attachmentEmbedKind(attachment) == "image" {
		return "!" + link
	}
	return link
}

// deleteMCPAttachment is the delete_attachment tool: an issue's attachment,
// or one on a document the actor can edit. An uploaded file is deleted too.
func (s *server) deleteMCPAttachment(ctx context.Context, actor mcpActor, data domain.Bootstrap, id string) (any, error) {
	q, err := s.mcpIssueQuery(ctx, actor)
	if err != nil {
		return nil, err
	}
	if issueID, err := s.store.IssueAttachmentParent(ctx, q, id); err == nil && issueID != "" {
		if _, err := invokeJSONHandler(ctx, http.MethodDelete, map[string]string{"id": issueID, "attachmentId": id}, nil, s.deleteAttachment); err != nil {
			return nil, err
		}
		return map[string]any{"deleted": true, "id": id}, nil
	}
	index := slices.IndexFunc(data.Documents, func(document domain.Document) bool {
		return slices.ContainsFunc(document.Attachments, func(attachment domain.Attachment) bool { return attachment.ID == id })
	})
	if index < 0 {
		return nil, fmt.Errorf("attachment not found")
	}
	target := mcpAttachmentTarget{document: data.Documents[index]}
	removed, err := s.deleteDocumentAttachment(ctx, actor.WorkspaceKey, target.document.ID, id)
	if err != nil {
		return nil, target.writeError(err)
	}
	if name := uploadNameFromURL(removed.URL); name != "" {
		if storage, err := s.storage(); err == nil {
			_ = storage.Delete(context.WithoutCancel(ctx), name)
		}
	}
	return map[string]any{"deleted": true, "id": id}, nil
}
