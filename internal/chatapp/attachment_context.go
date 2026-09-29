package chatapp

// DefaultNativeTextContextBytes is the conservative inline allowance when a
// route does not publish its context window. It is not an upload limit.
const DefaultNativeTextContextBytes = 64 << 10

// NativeTextContextBudget separates storage admission from inline disclosure.
// Counting one UTF-8 byte per token deliberately overestimates ordinary source
// text without importing a vendor tokenizer. Leave a quarter of the advertised
// window plus 4096 tokens for response/protocol overhead, then account for the
// ordinary conversation. This is a conservative estimate, not a provider token
// guarantee; unknown windows use a documented fallback.
func NativeTextContextBudget(maxContextTokens, ordinaryBytes int) int {
	budget := DefaultNativeTextContextBytes
	if maxContextTokens > 0 {
		budget = maxContextTokens - maxContextTokens/4 - 4096
	}
	budget = min(budget, int(MaxMessageAttachmentBytes))
	return max(0, budget-max(0, ordinaryBytes))
}

// NativeAttachmentToolContextBytes bounds retained private excerpts rather than
// file size. Older excerpts can be explicitly omitted and read again. Ordinary
// task context has its own runtime limits; this does not budget the whole task.
func NativeAttachmentToolContextBytes(maxContextTokens int) int {
	if maxContextTokens <= 0 {
		return DefaultNativeTextContextBytes
	}
	return min(DefaultNativeTextContextBytes, maxContextTokens/4)
}
