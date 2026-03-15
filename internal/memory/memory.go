package memory

import (
	"strings"
)

// triggerPhrases are natural language phrases that signal "save this to memory".
var triggerPhrases = []string{
	"remember that ",
	"remember: ",
	"remember, ",
	"please remember ",
	"save to memory: ",
	"save to memory ",
	"save this to memory",
	"note that ",
	"note: ",
	"keep in mind ",
	"keep in mind: ",
	"don't forget ",
	"don't forget that ",
	"store this: ",
	"store in memory: ",
	"add to memory: ",
}

// DetectAndExtract checks if the input is a memory-save request.
// Returns (fact, true) if it is, or ("", false) if not.
func DetectAndExtract(input string) (string, bool) {
	lower := strings.ToLower(strings.TrimSpace(input))

	for _, phrase := range triggerPhrases {
		if strings.HasPrefix(lower, phrase) {
			fact := strings.TrimSpace(input[len(phrase):])
			if fact != "" {
				return fact, true
			}
		}
	}

	return "", false
}

// BuildSystemPrompt constructs the system prompt injected before every chat.
// It includes Billy's persona and any stored memories.
func BuildSystemPrompt(memories []string) string {
	var sb strings.Builder

	sb.WriteString("You are Billy, a helpful AI coding assistant running locally. ")
	sb.WriteString("You are knowledgeable, concise, and prefer practical examples. ")
	sb.WriteString("You can help with code, debugging, architecture, and general questions. ")
	sb.WriteString("Format code in markdown code blocks with the language specified.")

	if len(memories) > 0 {
		sb.WriteString("\n\nThings I know about the user:\n")
		for _, m := range memories {
			sb.WriteString("- ")
			sb.WriteString(m)
			sb.WriteString("\n")
		}
		sb.WriteString("\nUse this context naturally in your responses when relevant.")
	}

	return sb.String()
}
