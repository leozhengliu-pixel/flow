package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// transcribe_media: audio goes to an OpenAI-compatible POST {base}/audio/transcriptions (multipart, verbose_json,
// timestamps from the segments); video goes to a multimodal model through POST {base}/chat/completions with the video
// as a video_url content part holding a base64 data URL (the shape vLLM, Qwen-VL/DashScope and Gemini's
// OpenAI-compatible endpoint accept). See FLOW_AGENT_MEDIA_* in docs/configuration.md.

const agentMediaNotEnabled = "Media transcription is not enabled on this server."

const agentVideoPrompt = "Describe this video for someone who cannot watch it. Say what happens over time with [mm:ss] timestamps, transcribe any text shown on screen exactly (quote it), and include any speech or notable sounds. Be factual and concise."

// agentMediaConfigured reports whether the model for this kind of media is set.
func (s *server) agentMediaConfigured(file domain.AgentMessageAttachment) bool {
	media := s.agent.Media
	if media.BaseURL == "" {
		return false
	}
	if agentVideoFile(file) {
		return media.VideoModel != ""
	}
	return media.TranscribeModel != ""
}

func (s *server) transcribeAgentMedia(ctx context.Context, file domain.AgentMessageAttachment, data []byte) (string, error) {
	timeout := s.agent.Media.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var text string
	var err error
	if agentVideoFile(file) {
		text, err = s.describeAgentVideo(ctx, file, data)
	} else {
		text, err = s.transcribeAgentAudio(ctx, file, data)
	}
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(text) == "" {
		return "No speech or description came back for this file.", nil
	}
	return limitAgentText(strings.TrimSpace(text)), nil
}

func (s *server) agentMediaDo(request *http.Request) ([]byte, error) {
	if key := s.agent.Media.APIKey; key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	client := s.agentClient
	if client == nil {
		client = newAgentHTTPClient()
	}
	response, err := client.Do(request)
	if err != nil {
		if request.Context().Err() != nil {
			return nil, fmt.Errorf("transcription timed out")
		}
		return nil, fmt.Errorf("the transcription service is unavailable")
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("the transcription service returned status %d: %s", response.StatusCode, truncateSettingsText(providerError(body), 300))
	}
	return body, nil
}

func (s *server) transcribeAgentAudio(ctx context.Context, file domain.AgentMessageAttachment, data []byte) (string, error) {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	_ = form.WriteField("model", s.agent.Media.TranscribeModel)
	_ = form.WriteField("response_format", "verbose_json")
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, agentMediaFileName(file.Name)))
	header.Set("Content-Type", firstNonEmpty(file.ContentType, "application/octet-stream"))
	part, err := form.CreatePart(header)
	if err != nil {
		return "", fmt.Errorf("could not prepare the audio")
	}
	_, _ = part.Write(data)
	if form.Close() != nil {
		return "", fmt.Errorf("could not prepare the audio")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, agentEndpoint(s.agent.Media.BaseURL, "/audio/transcriptions"), &body)
	if err != nil {
		return "", fmt.Errorf("could not create the transcription request")
	}
	request.Header.Set("Content-Type", form.FormDataContentType())
	raw, err := s.agentMediaDo(request)
	if err != nil {
		return "", err
	}
	var result struct {
		Text     string `json:"text"`
		Segments []struct {
			Start float64 `json:"start"`
			Text  string  `json:"text"`
		} `json:"segments"`
	}
	if json.Unmarshal(raw, &result) != nil {
		// Some servers answer a plain transcript.
		return string(raw), nil
	}
	var lines []string
	for _, segment := range result.Segments {
		if text := strings.TrimSpace(segment.Text); text != "" {
			lines = append(lines, "["+agentMediaTimestamp(segment.Start)+"] "+text)
		}
	}
	if len(lines) == 0 {
		if text := strings.TrimSpace(result.Text); text != "" {
			return "[00:00] " + text, nil
		}
		return "", nil
	}
	return strings.Join(lines, "\n"), nil
}

func (s *server) describeAgentVideo(ctx context.Context, file domain.AgentMessageAttachment, data []byte) (string, error) {
	mediaType := strings.ToLower(strings.TrimSpace(strings.SplitN(file.ContentType, ";", 2)[0]))
	if !strings.HasPrefix(mediaType, "video/") {
		mediaType = "video/mp4"
	}
	payload, err := json.Marshal(map[string]any{
		"model": s.agent.Media.VideoModel,
		"messages": []any{map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": agentVideoPrompt},
			map[string]any{"type": "video_url", "video_url": map[string]any{"url": "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(data)}},
		}}},
		"max_tokens": 2048,
	})
	if err != nil {
		return "", fmt.Errorf("could not prepare the video")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, agentEndpoint(s.agent.Media.BaseURL, "/chat/completions"), bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("could not create the transcription request")
	}
	request.Header.Set("Content-Type", "application/json")
	raw, err := s.agentMediaDo(request)
	if err != nil {
		return "", err
	}
	var result struct {
		Choices []struct {
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(raw, &result) != nil || len(result.Choices) == 0 {
		return "", fmt.Errorf("the video model returned an unexpected response")
	}
	content := result.Choices[0].Message.Content
	var text string
	if json.Unmarshal(content, &text) == nil {
		return text, nil
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &parts) == nil {
		var texts []string
		for _, part := range parts {
			texts = append(texts, part.Text)
		}
		return strings.Join(texts, "\n"), nil
	}
	return "", nil
}

func agentMediaFileName(name string) string {
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	return firstNonEmpty(name, "audio")
}

// agentMediaTimestamp formats seconds as mm:ss, or h:mm:ss from an hour on.
func agentMediaTimestamp(seconds float64) string {
	total := max(int(seconds), 0)
	if total >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", total/3600, total%3600/60, total%60)
	}
	return fmt.Sprintf("%02d:%02d", total/60, total%60)
}
