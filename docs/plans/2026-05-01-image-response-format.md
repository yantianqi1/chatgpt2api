# Image Response Format Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:test-driven-development to implement this plan task-by-task.

**Goal:** Add a live settings option that forces downstream image responses to contain only `url` or only `b64_json`.

**Architecture:** Store `image_response_format` in the existing settings pipeline and read it at request execution time. Force `/v1/images/*` and creation-task image handlers to set `response_format` from the current config, then make protocol formatting emit exactly one image field.

**Tech Stack:** Go backend config/http/protocol tests, Vite React settings UI, existing `/api/settings` persistence.

---

### Task 1: Backend Settings

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Steps:**
1. Add failing tests for default, persistence, live update, and invalid value rejection.
2. Run `go test ./internal/config` and confirm the new tests fail.
3. Add `CHATGPT2API_IMAGE_RESPONSE_FORMAT`, `ImageResponseFormat()`, and validation.
4. Run `go test ./internal/config`.

### Task 2: Protocol Output

**Files:**
- Modify: `internal/protocol/conversation.go`
- Test: `internal/protocol/api_test.go`

**Steps:**
1. Add failing tests proving `url` omits `b64_json` and `b64_json` omits `url`.
2. Run `go test ./internal/protocol` and confirm failure.
3. Update `FormatImageResult` to emit only the selected field.
4. Run `go test ./internal/protocol`.

### Task 3: HTTP and Creation Tasks

**Files:**
- Modify: `internal/httpapi/app.go`
- Test: `internal/config/config_test.go`

**Steps:**
1. Force image handlers and creation-task handlers to read `a.config.ImageResponseFormat()` at execution time.
2. Run `go test ./internal/httpapi ./internal/service`.

### Task 4: Settings UI

**Files:**
- Modify: `web/src/lib/api.ts`
- Modify: `web/src/app/settings/store.ts`
- Modify: `web/src/app/settings/components/config-card.tsx`

**Steps:**
1. Add `image_response_format` to the settings type and store normalization.
2. Add a select control in the settings card.
3. Run `cd web && npm run build`.
