package ethol

import "strings"

func parseCommand(text string) string {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "/") {
		fields := strings.Fields(text)
		if len(fields) == 0 {
			return ""
		}
		cmd := fields[0]
		if idx := strings.Index(cmd, "@"); idx != -1 {
			cmd = cmd[:idx]
		}
		return cmd
	}
	return commandTextAliases[strings.ToLower(text)]
}
