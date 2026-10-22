package redaction

func Values(values map[string]any) (map[string]any, []Finding) {
	var findings [][]Finding
	redacted := redactValue(values, &findings).(map[string]any)
	return redacted, Merge(findings...)
}

func redactValue(value any, findings *[][]Finding) any {
	switch v := value.(type) {
	case string:
		result := Text(v)
		*findings = append(*findings, result.Findings)
		return result.Text
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[key] = redactValue(item, findings)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = redactValue(item, findings)
		}
		return out
	default:
		return value
	}
}
