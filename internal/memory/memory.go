package memory

import (
	"strings"
)

// triggerPhrases are natural language phrases that signal "save this to memory".
// Each entry is a lowercase prefix; the fact is everything after the prefix.
var triggerPhrases = []string{
	// Direct save / remember
	"remember that ",
	"remember: ",
	"remember, ",
	"please remember that ",
	"please remember ",
	"save to memory: ",
	"save to memory ",
	"save this to memory",
	"note that ",
	"note: ",
	"keep in mind that ",
	"keep in mind ",
	"keep in mind: ",
	"don't forget that ",
	"don't forget ",
	"store this: ",
	"store in memory: ",
	"add to memory: ",
	// Natural language "can you / i want you to" patterns
	"can you save a memory that ",
	"can you save a memory ",
	"can you remember that ",
	"can you remember ",
	"can you save that ",
	"can you note that ",
	"can you note ",
	"save a memory that ",
	"save a memory ",
	"save a note that ",
	"save a note ",
	"i want you to remember that ",
	"i want you to remember ",
	"i'd like you to remember that ",
	"i'd like you to remember ",
	"i'd like you to save that ",
	"please save a memory that ",
	"please save a memory ",
	"please save that ",
	"please note that ",
	"please note ",
	// Preference / info save patterns
	"save my preference that ",
	"save my preference ",
	"my preference is ",
	"for future reference, ",
	"for future reference: ",
	"fyi: ",
	"fyi, ",
}

// containsTriggers are substrings that, combined with a save-like intent,
// indicate a memory request anywhere in the sentence.
var containsTriggers = []struct {
	verb    string
	extract string // prefix to strip when extracting the fact after "that"
}{
	{"save a memory", "save a memory that "},
	{"save a note", "save a note that "},
	{"save that i", ""},
	{"remember that i", ""},
}

// DetectAndExtract checks if the input is a memory-save request.
// Returns (fact, true) if it is, or ("", false) if not.
func DetectAndExtract(input string) (string, bool) {
	lower := strings.ToLower(strings.TrimSpace(input))

	// 1. Exact prefix match (fast path)
	for _, phrase := range triggerPhrases {
		if strings.HasPrefix(lower, phrase) {
			fact := strings.TrimSpace(input[len(phrase):])
			if fact != "" {
				return fact, true
			}
		}
	}

	// 2. Contains-based fallback — catches "Can you save a memory that..."
	for _, ct := range containsTriggers {
		if strings.Contains(lower, ct.verb) {
			// Try to extract the fact after "that " if present
			if idx := strings.Index(lower, " that "); idx != -1 {
				fact := strings.TrimSpace(input[idx+6:])
				if fact != "" {
					return fact, true
				}
			}
			// Fall back to everything after the verb
			if idx := strings.Index(lower, ct.verb); idx != -1 {
				fact := strings.TrimSpace(input[idx+len(ct.verb):])
				if fact != "" {
					return fact, true
				}
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
	sb.WriteString("Format code in markdown code blocks with the language specified.\n\n")
	sb.WriteString("IMPORTANT — Billy has a built-in memory system. ")
	sb.WriteString("When the user asks you to save, remember, or note something about themselves or their preferences, ")
	sb.WriteString("Billy's memory system has ALREADY saved it automatically before this message reached you. ")
	sb.WriteString("Simply confirm warmly (e.g. \"Got it! I'll keep that in mind.\") — do NOT suggest shell commands, ")
	sb.WriteString("alias tricks, or config file edits for saving personal preferences. Never generate shell commands for memory requests.")

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
