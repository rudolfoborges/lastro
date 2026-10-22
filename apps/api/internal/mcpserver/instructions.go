package mcpserver

import (
	"context"
	"embed"
	"fmt"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

//go:embed instructions/*.md
var instructionFiles embed.FS

type topic struct {
	name        string
	prompt      string
	description string
}

var topics = []topic{
	{name: "conversation", prompt: "conversation_procedure", description: "How to record, confirm and correct expenses Rudolfo reports in the conversation"},
	{name: "classification", prompt: "classification_policy", description: "Taxonomy and rules to classify expenses into categories and purpose"},
}

type GetInstructionsInput struct {
	Topic string `json:"topic" jsonschema:"which procedure to read"`
}

type GetInstructionsOutput struct {
	Topic   string `json:"topic"`
	Content string `json:"content" jsonschema:"the procedure in Markdown"`
}

func (s *Server) registerInstructions() {
	names := make([]any, len(topics))
	for i, t := range topics {
		names[i] = t.name
	}
	addTool(s, toolSpec{
		name:        "get_instructions",
		title:       "Read a Lastro procedure",
		description: "Returns one of Case's procedures for working with Lastro, in Markdown: conversation (recording and correcting expenses) or classification (categories and purpose). Read conversation before recording and classification before classifying.",
		readOnly:    true,
		idempotent:  true,
		enums:       map[string][]any{"topic": names},
	}, func(_ context.Context, _ call, in GetInstructionsInput) (GetInstructionsOutput, error) {
		content, err := instructionsFor(in.Topic)
		if err != nil {
			return GetInstructionsOutput{}, err
		}
		return GetInstructionsOutput{Topic: in.Topic, Content: content}, nil
	})
	for _, t := range topics {
		s.mcp.AddPrompt(&mcp.Prompt{Name: t.prompt, Title: t.description, Description: t.description}, func(context.Context, *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			content, err := instructionsFor(t.name)
			if err != nil {
				return nil, err
			}
			return &mcp.GetPromptResult{
				Description: t.description,
				Messages:    []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: content}}},
			}, nil
		})
	}
}

func instructionsFor(name string) (string, error) {
	content, err := instructionFiles.ReadFile("instructions/" + name + ".md")
	if err != nil {
		known := make([]string, len(topics))
		for i, t := range topics {
			known[i] = t.name
		}
		sort.Strings(known)
		return "", invalid("topic", fmt.Sprintf("unknown topic %q; use one of %s", name, strings.Join(known, ", ")))
	}
	return string(content), nil
}
