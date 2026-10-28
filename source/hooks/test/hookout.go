package test

import "encoding/json"

// AddedContext is the additionalContext a hook printed on stdout for event,
// empty where it printed none or answered another event.
func AddedContext(out string, event string) string {
	var answer struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}

	err := json.Unmarshal([]byte(out), &answer)
	if err != nil || answer.HookSpecificOutput.HookEventName != event {
		return ""
	}

	return answer.HookSpecificOutput.AdditionalContext
}
