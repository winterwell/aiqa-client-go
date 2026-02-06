package aiqa

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
)

func TraceBedrockConverse(ctx context.Context, converseInput *bedrockruntime.ConverseInput, converseOutput *bedrockruntime.ConverseOutput) {
	if converseInput != nil {
		if len(converseInput.Messages) > 0 {
			SetSpanAttribute(ctx, "gen_ai.request.messages", converseInput.Messages)
		}
	}

	if converseOutput != nil {
		usage := converseOutput.Usage
		if usage != nil {
			SetTokenUsage(ctx, usage.InputTokens, usage.OutputTokens, usage.TotalTokens, usage.CacheReadInputTokens)
		}
	}
}
