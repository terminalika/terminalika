package webhook

import (
	"encoding/json"
	"strings"

	"github.com/terminalika/terminalika/internal/agents"
)

// HookInput is the subset of the JSON that agent hook systems pipe to a
// hook command's stdin. Claude Code sends hook_event_name ("Stop",
// "Notification", ...) plus notification_type/message for notifications;
// Cursor's hooks use the same hook_event_name field with lower-case names
// ("stop").
type HookInput struct {
	HookEventName    string `json:"hook_event_name"`
	NotificationType string `json:"notification_type"`
	Message          string `json:"message"`
	StopHookActive   bool   `json:"stop_hook_active"`
}

// InferKind maps a hook's stdin JSON to an event kind, so `terminalika
// notify` can be wired to an agent's hooks without spelling the kind out
// per hook. ok is false for hooks that don't correspond to either kind
// (subagent stops, tool-use hooks, ...), which the caller should ignore.
func InferKind(data []byte) (kind agents.EventKind, detail string, ok bool) {
	var in HookInput
	if err := json.Unmarshal(data, &in); err != nil {
		return 0, "", false
	}
	switch strings.ToLower(in.HookEventName) {
	case "stop", "afteragentresponse":
		return agents.Finished, "", true
	case "notification":
		switch strings.ToLower(in.NotificationType) {
		case "permission_prompt", "idle_prompt", "elicitation_dialog", "":
			return agents.InputRequired, in.Message, true
		}
		// Other notification types (auth success, ...) are informational.
		return 0, "", false
	}
	return 0, "", false
}
