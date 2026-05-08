package protocol

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"chatgpt2api/internal/backend"
	"chatgpt2api/internal/service"
	"chatgpt2api/internal/storage"
	"chatgpt2api/internal/util"
)

type protocolTestImageConfig struct {
	baseURL string
	root    string
}

type protocolTestAccountConfig struct{}

func (protocolTestAccountConfig) AutoRemoveInvalidAccounts() bool     { return true }
func (protocolTestAccountConfig) AutoRemoveRateLimitedAccounts() bool { return false }
func (protocolTestAccountConfig) Proxy() string                       { return "" }

func (c protocolTestImageConfig) ImagesDir() string {
	return filepath.Join(c.root, "images")
}

func (c protocolTestImageConfig) ImageMetadataDir() string {
	return filepath.Join(c.root, "image_metadata")
}

func (c protocolTestImageConfig) BaseURL() string {
	return c.baseURL
}

func (c protocolTestImageConfig) CleanupOldImages() int {
	return 0
}

func TestChatAndResponsesImageParsing(t *testing.T) {
	imageData := base64.StdEncoding.EncodeToString([]byte("png-bytes"))
	body := map[string]any{
		"model": "gpt-image-2",
		"messages": []any{
			map[string]any{"role": "system", "content": "ignore"},
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "text", "text": "画一张图"},
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64," + imageData}},
			}},
		},
		"n": 2,
	}

	model, prompt, n, images, messages, err := ChatImageArgs(body)
	if err != nil {
		t.Fatalf("ChatImageArgs() error = %v", err)
	}
	if model != "gpt-image-2" || prompt != "画一张图" || n != 2 {
		t.Fatalf("ChatImageArgs() = model %q prompt %q n %d", model, prompt, n)
	}
	if len(messages) != 2 || messages[1]["content"] != "画一张图" {
		t.Fatalf("messages = %#v", messages)
	}
	if len(images) != 1 || string(images[0].Data) != "png-bytes" || images[0].ContentType != "image/png" {
		t.Fatalf("images = %#v", images)
	}

	responseInput := []any{
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "input_text", "text": "生成封面"},
			map[string]any{"type": "input_image", "image_url": "data:image/png;base64," + imageData},
		}},
	}
	if prompt := ExtractResponsePrompt(responseInput); prompt != "生成封面" {
		t.Fatalf("ExtractResponsePrompt() = %q", prompt)
	}
	if image := ExtractResponseImage(responseInput); image == nil || string(image.Data) != "png-bytes" {
		t.Fatalf("ExtractResponseImage() = %#v", image)
	}
}

func TestImageRequestDefaultsToAutoModel(t *testing.T) {
	body := map[string]any{
		"messages": []any{
			map[string]any{"role": "user", "content": "画一张图"},
		},
	}
	model, prompt, n, _, _, err := ChatImageArgs(body)
	if err != nil {
		t.Fatalf("ChatImageArgs() error = %v", err)
	}
	if model != "auto" || prompt != "画一张图" || n != 1 {
		t.Fatalf("ChatImageArgs() = model %q prompt %q n %d", model, prompt, n)
	}
}

func TestFormatImageResultReturnsOnlyRequestedImageField(t *testing.T) {
	imageData := base64.StdEncoding.EncodeToString([]byte("image-bytes"))
	engine := &Engine{Config: protocolTestImageConfig{baseURL: "https://assets.test", root: t.TempDir()}}

	b64Result := engine.FormatImageResult([]map[string]any{{"b64_json": imageData}}, "draw", "b64_json", "", "owner", 123, "")
	b64Data := b64Result["data"].([]map[string]any)
	if b64Data[0]["b64_json"] != imageData {
		t.Fatalf("b64_json result = %#v", b64Data[0])
	}
	if _, ok := b64Data[0]["url"]; ok {
		t.Fatalf("b64_json response leaked url: %#v", b64Data[0])
	}

	urlResult := engine.FormatImageResult([]map[string]any{{"b64_json": imageData}}, "draw", "url", "", "owner", 123, "")
	urlData := urlResult["data"].([]map[string]any)
	if got := urlData[0]["url"]; got == "" {
		t.Fatalf("url result = %#v", urlData[0])
	}
	if _, ok := urlData[0]["b64_json"]; ok {
		t.Fatalf("url response leaked b64_json: %#v", urlData[0])
	}
}

func TestImageChatConversationRequestUsesRequestedImageFormat(t *testing.T) {
	body := map[string]any{
		"model":           "gpt-image-2",
		"response_format": "url",
		"base_url":        "https://assets.test",
		"messages": []any{
			map[string]any{"role": "user", "content": "画一张图"},
		},
	}

	model, prompt, n, images, messages, err := ChatImageArgs(body)
	if err != nil {
		t.Fatalf("ChatImageArgs() error = %v", err)
	}
	request := imageChatConversationRequest(body, imageChatConversationOptions{
		model:    model,
		prompt:   prompt,
		n:        n,
		images:   images,
		messages: messages,
	})

	if request.ResponseFormat != "url" {
		t.Fatalf("ResponseFormat = %q, want url", request.ResponseFormat)
	}
	if request.BaseURL != "https://assets.test" {
		t.Fatalf("BaseURL = %q, want configured base url", request.BaseURL)
	}
}

func TestBuildChatImageMarkdownContentRendersURLImages(t *testing.T) {
	content := BuildChatImageMarkdownContent(map[string]any{
		"data": []map[string]any{
			{"url": "https://assets.test/images/generated.png"},
		},
	})

	if content != "![image_1](https://assets.test/images/generated.png)" {
		t.Fatalf("content = %q, want markdown image url", content)
	}
	if strings.Contains(content, "base64") || strings.Contains(content, "data:") {
		t.Fatalf("content leaked inline image data: %q", content)
	}
}

func TestTextModelDoesNotForceImageChatRoute(t *testing.T) {
	if IsImageChatRequest(map[string]any{"model": "gpt-5", "messages": []any{map[string]any{"role": "user", "content": "hello"}}}) {
		t.Fatal("gpt-5 text chat should not be routed as an image request without image modality")
	}
	if !IsImageChatRequest(map[string]any{"model": "gpt-5", "modalities": []any{"image"}, "messages": []any{map[string]any{"role": "user", "content": "draw"}}}) {
		t.Fatal("gpt-5 with image modality should be routed as an image request")
	}
}

func TestListModelsUsesInjectedLister(t *testing.T) {
	called := false
	engine := &Engine{
		ListModelsFunc: func(context.Context) (map[string]any, error) {
			called = true
			return map[string]any{
				"object": "list",
				"data": []map[string]any{
					{"id": "custom-model", "object": "model"},
				},
			}, nil
		},
	}

	result, err := engine.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if !called {
		t.Fatal("ListModelsFunc was not called")
	}
	data, _ := result["data"].([]map[string]any)
	if len(data) == 0 || data[0]["id"] != "custom-model" {
		t.Fatalf("ListModels() data = %#v", result["data"])
	}
}

func TestImageContextPromptIncludesHistory(t *testing.T) {
	messages := []map[string]any{
		{"role": "system", "content": "保持水彩风格"},
		{"role": "user", "content": "画一只白猫"},
		{"role": "assistant", "content": "Generated image: 白猫坐在窗边"},
		{"role": "user", "content": "把它改成夜晚"},
	}
	prompt := BuildImageContextPrompt(messages, LatestUserPrompt(messages), "1:1", "high")
	for _, want := range []string{"保持水彩风格", "画一只白猫", "白猫坐在窗边", "当前请求:\n把它改成夜晚", "输出为 1:1", "画质使用 High 档"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("context prompt missing %q: %s", want, prompt)
		}
	}
}

func TestBuildImagePromptIncludesThreeTwoAndQualityHints(t *testing.T) {
	prompt := BuildImagePrompt("画一张产品照片", "3:2", "medium")
	for _, want := range []string{"画一张产品照片", "3:2 横版构图", "画质使用 Medium 档"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("image prompt missing %q: %s", want, prompt)
		}
	}
}

func TestBuildImagePromptIncludesExactResolutionHint(t *testing.T) {
	prompt := BuildImagePrompt("画一张城市海报", "3840x2160", "high")
	for _, want := range []string{"画一张城市海报", "3840 x 2160 像素", "画质使用 High 档"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("image prompt missing %q: %s", want, prompt)
		}
	}
}

func TestRequiresPaidImageSize(t *testing.T) {
	tests := []struct {
		name string
		size string
		want bool
	}{
		{name: "empty", size: "", want: false},
		{name: "aspect ratio", size: "16:9", want: false},
		{name: "free pixel budget", size: "1248x1248", want: false},
		{name: "1080p square below paid budget", size: "1080x1080", want: false},
		{name: "1080p widescreen above paid budget", size: "1920x1080", want: true},
		{name: "2k", size: "2560x1440", want: true},
		{name: "4k", size: "3840x2160", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RequiresPaidImageSize(tt.size); got != tt.want {
				t.Fatalf("RequiresPaidImageSize(%q) = %v, want %v", tt.size, got, tt.want)
			}
		})
	}
}

func TestResponsesInputKeepsAssistantAndGeneratedImageContext(t *testing.T) {
	imageData := base64.StdEncoding.EncodeToString([]byte("previous-image"))
	input := []any{
		map[string]any{"type": "message", "role": "assistant", "content": []any{
			map[string]any{"type": "output_text", "text": "上一轮说明"},
		}},
		map[string]any{"type": "image_generation_call", "status": "completed", "result": imageData, "revised_prompt": "一只红色猫"},
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "input_text", "text": "把它改成蓝色"},
		}},
	}
	messages := MessagesFromInput(input, "保持同一角色")
	if len(messages) != 4 {
		t.Fatalf("MessagesFromInput() = %#v", messages)
	}
	if messages[0]["role"] != "system" || messages[1]["role"] != "assistant" || messages[2]["role"] != "assistant" || messages[3]["role"] != "user" {
		t.Fatalf("unexpected message roles: %#v", messages)
	}
	if got := LatestUserPrompt(messages); got != "把它改成蓝色" {
		t.Fatalf("LatestUserPrompt() = %q", got)
	}
	images := ExtractResponseImages(input)
	if len(images) != 1 || string(images[0].Data) != "previous-image" {
		t.Fatalf("ExtractResponseImages() = %#v", images)
	}
}

func TestToolCallParsing(t *testing.T) {
	text := `先处理
<tool_calls><tool_call><tool_name>read_file</tool_name><parameters><path><![CDATA[internal/app.go]]></path><limit>5</limit></parameters></tool_call></tool_calls>`
	calls := ParseToolCalls(text)
	if len(calls) != 1 {
		t.Fatalf("ParseToolCalls() = %#v", calls)
	}
	if calls[0].Name != "read_file" {
		t.Fatalf("tool name = %q", calls[0].Name)
	}
	if calls[0].Input["path"] != "internal/app.go" || calls[0].Input["limit"] != float64(5) {
		t.Fatalf("tool input = %#v", calls[0].Input)
	}
	if visible := StreamableText(text); visible != "先处理" {
		t.Fatalf("StreamableText() = %q", visible)
	}
	if stripped := StripToolMarkup(text); stripped != "先处理" {
		t.Fatalf("StripToolMarkup() = %q", stripped)
	}
}

func TestStreamImageResponseErrorsWhenNoImageOutput(t *testing.T) {
	outputs := make(chan ImageOutput)
	close(outputs)
	events, errCh := StreamImageResponse(outputs, "draw", "gpt-image-2")
	var count int
	for range events {
		count++
	}
	if count != 1 {
		t.Fatalf("event count = %d, want response.created only", count)
	}
	if err := <-errCh; err == nil || err.Error() != "image generation failed" {
		t.Fatalf("err = %v", err)
	}
}

func TestCollectImageOutputsMarksTextResponse(t *testing.T) {
	outputs := make(chan ImageOutput)
	close(outputs)
	errCh := make(chan error, 1)
	errCh <- &ImageGenerationError{Message: "text response", StatusCode: 400, Type: "invalid_request_error", Code: "image_generation_text_response"}
	close(errCh)

	result, err := (&Engine{}).CollectImageOutputs(outputs, errCh)
	if err == nil {
		t.Fatal("CollectImageOutputs() err = nil, want text response error")
	}
	if result["output_type"] != "text" {
		t.Fatalf("output_type = %#v, want text in %#v", result["output_type"], result)
	}
	if result["message"] != "text response" {
		t.Fatalf("message = %#v, want text response", result["message"])
	}
}

func TestStreamTextResponseEventsPropagatesUpstreamError(t *testing.T) {
	deltas := make(chan string, 1)
	upstreamErr := make(chan error, 1)
	deltas <- "partial"
	close(deltas)
	upstreamErr <- errors.New("upstream failed")
	close(upstreamErr)

	events, errCh := streamTextResponseEvents(context.Background(), "auto", deltas, upstreamErr)
	var types []string
	for event := range events {
		if eventType, ok := event["type"].(string); ok {
			types = append(types, eventType)
		}
	}
	if err := <-errCh; err == nil || err.Error() != "upstream failed" {
		t.Fatalf("err = %v, want upstream failed", err)
	}
	for _, eventType := range types {
		if eventType == "response.completed" || eventType == "response.output_text.done" {
			t.Fatalf("unexpected completion event after upstream error: %v", types)
		}
	}
}

func TestHandleImageGenerationsValidatesPromptAndCount(t *testing.T) {
	engine := &Engine{}
	for _, tc := range []struct {
		name string
		body map[string]any
		want string
	}{
		{name: "empty prompt", body: map[string]any{"n": 1}, want: "prompt is required"},
		{name: "too many images", body: map[string]any{"prompt": "draw", "n": 5}, want: "n must be between 1 and 4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := engine.HandleImageGenerations(context.Background(), tc.body)
			var httpErr HTTPError
			if !errors.As(err, &httpErr) {
				t.Fatalf("err = %T %v, want HTTPError", err, err)
			}
			if httpErr.Status != 400 || httpErr.Message != tc.want {
				t.Fatalf("HTTPError = %#v, want status 400 message %q", httpErr, tc.want)
			}
		})
	}
}

func TestHandleChatCompletionsRetriesAfterInvalidTextAccount(t *testing.T) {
	var mu sync.Mutex
	var requirementTokens []string
	var conversationTokens []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		switch r.URL.Path {
		case "/":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("<html>ok</html>"))
		case "/backend-api/sentinel/chat-requirements":
			mu.Lock()
			requirementTokens = append(requirementTokens, token)
			mu.Unlock()
			if token == "token-invalid" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"detail":"token_invalidated"}`))
				return
			}
			if token == "token-valid" {
				util.WriteJSON(w, http.StatusOK, map[string]any{"token": "chat-token"})
				return
			}
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"detail":"unexpected token"}`))
		case "/backend-api/conversation":
			mu.Lock()
			conversationTokens = append(conversationTokens, token)
			mu.Unlock()
			if token != "token-valid" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"detail":"token_invalidated"}`))
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, ok := w.(http.Flusher)
			if !ok {
				t.Error("response writer does not support streaming")
				return
			}
			_, _ = fmt.Fprintln(w, `data: {"message":{"author":{"role":"assistant"},"content":{"parts":["模型正常返回"]}},"conversation_id":"conv-1"}`)
			flusher.Flush()
			_, _ = fmt.Fprintln(w, "data: [DONE]")
			flusher.Flush()
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	proxy := service.NewProxyService(protocolTestAccountConfig{})
	accounts := service.NewAccountService(
		storage.NewJSONBackend(filepath.Join(dir, "accounts.json"), filepath.Join(dir, "auth_keys.json")),
		protocolTestAccountConfig{},
		proxy,
		service.NewLogService(dir),
	)
	accounts.AddAccounts([]string{"token-invalid", "token-valid"})

	engine := &Engine{
		Accounts: accounts,
		Proxy:    proxy,
		TextBackendFunc: func(accessToken string) *backend.Client {
			client := backend.NewClient(accessToken, accounts, proxy)
			client.BaseURL = server.URL
			return client
		},
	}

	result, _, err := engine.HandleChatCompletions(context.Background(), map[string]any{
		"model": "gpt-5.5",
		"messages": []any{
			map[string]any{"role": "user", "content": "你好"},
		},
	})
	if err != nil {
		t.Fatalf("HandleChatCompletions() error = %v", err)
	}
	choices, ok := result["choices"].([]map[string]any)
	if !ok || len(choices) != 1 {
		t.Fatalf("choices = %#v", result["choices"])
	}
	message := choices[0]["message"].(map[string]any)
	if message["content"] != "模型正常返回" {
		t.Fatalf("message content = %#v, want 模型正常返回", message["content"])
	}
	if account := accounts.GetAccount("token-invalid"); account != nil {
		t.Fatalf("invalid account still present: %#v", account)
	}
	if account := accounts.GetAccount("token-valid"); account == nil {
		t.Fatal("valid account was removed unexpectedly")
	}
	if got := requirementTokens; len(got) != 2 || got[0] != "token-invalid" || got[1] != "token-valid" {
		t.Fatalf("requirement token order = %#v, want [token-invalid token-valid]", got)
	}
	if got := conversationTokens; len(got) != 1 || got[0] != "token-valid" {
		t.Fatalf("conversation token order = %#v, want [token-valid]", got)
	}
}

func TestMergeSystemUsesCompactToolRuleForClaudeCode(t *testing.T) {
	merged := MergeSystem("You are Claude Code, an agent.", "Available tools:\nTool: read_file\n\nTool use rules:\nverbose")
	text, ok := merged.(string)
	if !ok {
		t.Fatalf("MergeSystem() = %T, want string", merged)
	}
	if strings.Contains(text, "Available tools:") {
		t.Fatalf("MergeSystem() kept verbose tool prompt: %q", text)
	}
	if !strings.Contains(text, "Tool output adapter") || !strings.Contains(text, "<tool_calls>") {
		t.Fatalf("MergeSystem() missing compact XML rule: %q", text)
	}
}
