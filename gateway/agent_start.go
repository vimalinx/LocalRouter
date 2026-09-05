package main

import (
	_ "embed"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
)

//go:embed agent-start.md
var agentStartGuide string

func handleAgentStartGuide(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, "text/markdown; charset=utf-8", []byte(agentStartGuide))
}

var legacyTokenValueExample = regexp.MustCompile(`\$\{?LOCALROUTER_API_TOKEN(?:\}|\b)`)

// Old authored sources remain digest-owned. Publishing does not repeat obsolete
// raw-Token instructions: omit those examples and direct readers to the current
// generated operation contract. The original source stays available to its owner.
func currentPublishedGuide(markdown string) string {
	const replacement = "> 旧的原值 Token 示例已停用。请使用生成的操作参考中的 lr call，通过 LOCALROUTER_API_TOKEN_FILE 指定权限为 0600 的私有文件。\n"
	var output strings.Builder
	var block []string
	fence := ""
	flush := func() {
		text := strings.Join(block, "\n")
		if legacyTokenValueExample.MatchString(text) {
			output.WriteString(replacement)
		} else {
			output.WriteString(text + "\n")
		}
		block = nil
	}
	for _, line := range strings.Split(markdown, "\n") {
		trim := strings.TrimSpace(line)
		if fence != "" {
			block = append(block, line)
			if strings.HasPrefix(trim, fence) {
				flush()
				fence = ""
			}
		} else if strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") {
			fence = trim[:3]
			block = []string{line}
		} else if legacyTokenValueExample.MatchString(line) {
			output.WriteString(replacement)
		} else {
			output.WriteString(line + "\n")
		}
	}
	if len(block) > 0 {
		flush()
	}
	return strings.TrimRight(output.String(), "\n") + "\n"
}
