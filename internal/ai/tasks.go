package ai

import (
	"context"
	"encoding/json"
)

// Task-level functions: the only AI entry points domain code uses.

func (r *Router) Draft(ctx context.Context, orgID, sensitivity string, in DraftInput) (DraftOutput, Result, error) {
	res, err := r.Run(ctx, Task{
		Kind: "draft", PromptID: "draft.answer@3", Input: in, Sensitivity: sensitivity, OrgID: orgID, MaxTokens: 900,
		Validate: func(raw json.RawMessage) (any, error) {
			var out DraftOutput
			if err := decodeStrict(raw, &out); err != nil {
				return nil, err
			}
			return out, out.validate(in)
		},
		Demo: func() (any, error) { return DemoDraft(in), nil },
	})
	if err != nil {
		return DraftOutput{}, res, err
	}
	return res.Output.(DraftOutput), res, nil
}

func (r *Router) SMEQuestion(ctx context.Context, orgID, sensitivity string, in SMEQuestionInput) (SMEQuestionOutput, Result, error) {
	res, err := r.Run(ctx, Task{
		Kind: "sme_question", PromptID: "sme.question@1", Input: in, Sensitivity: sensitivity, OrgID: orgID, MaxTokens: 300,
		Validate: func(raw json.RawMessage) (any, error) {
			var out SMEQuestionOutput
			if err := decodeStrict(raw, &out); err != nil {
				return nil, err
			}
			if out.Question == "" {
				return nil, errEmpty("question")
			}
			return out, nil
		},
		Demo: func() (any, error) { return DemoSMEQuestion(in), nil },
	})
	if err != nil {
		return SMEQuestionOutput{}, res, err
	}
	return res.Output.(SMEQuestionOutput), res, nil
}

func (r *Router) Classify(ctx context.Context, orgID, sensitivity string, in ClassifyInput) (ClassifyOutput, Result, error) {
	res, err := r.Run(ctx, Task{
		Kind: "classify", PromptID: "classify.requirement@1", Input: in, Sensitivity: sensitivity, OrgID: orgID, MaxTokens: 100,
		Validate: func(raw json.RawMessage) (any, error) {
			var out ClassifyOutput
			if err := decodeStrict(raw, &out); err != nil {
				return nil, err
			}
			return out, out.validate(in)
		},
		Demo: func() (any, error) { return DemoClassify(in), nil },
	})
	if err != nil {
		return ClassifyOutput{}, res, err
	}
	return res.Output.(ClassifyOutput), res, nil
}

func (r *Router) Extract(ctx context.Context, orgID, sensitivity string, in ExtractInput) (ExtractOutput, Result, error) {
	res, err := r.Run(ctx, Task{
		Kind: "extract", PromptID: "extract.requirements@2", Input: in, Sensitivity: sensitivity, OrgID: orgID, MaxTokens: 4000,
		Validate: func(raw json.RawMessage) (any, error) {
			var out ExtractOutput
			if err := decodeStrict(raw, &out); err != nil {
				return nil, err
			}
			return out, out.validate(in)
		},
		Demo: func() (any, error) { return DemoExtract(in), nil },
	})
	if err != nil {
		return ExtractOutput{}, res, err
	}
	return res.Output.(ExtractOutput), res, nil
}

func (r *Router) VerifyClaims(ctx context.Context, orgID, sensitivity string, in VerifyInput) (VerifyOutput, Result, error) {
	res, err := r.Run(ctx, Task{
		Kind: "verify", PromptID: "verify.claims@1", Input: in, Sensitivity: sensitivity, OrgID: orgID, MaxTokens: 600,
		Validate: func(raw json.RawMessage) (any, error) {
			var out VerifyOutput
			if err := decodeStrict(raw, &out); err != nil {
				return nil, err
			}
			return out, nil
		},
		Demo: func() (any, error) { return VerifyOutput{Flags: []VerifyFlag{}}, nil },
	})
	if err != nil {
		return VerifyOutput{}, res, err
	}
	return res.Output.(VerifyOutput), res, nil
}

func (r *Router) Summarize(ctx context.Context, orgID, sensitivity string, in SummarizeInput) (SummarizeOutput, Result, error) {
	res, err := r.Run(ctx, Task{
		Kind: "summarize", PromptID: "summarize.text@1", Input: in, Sensitivity: sensitivity, OrgID: orgID, MaxTokens: 200,
		Validate: func(raw json.RawMessage) (any, error) {
			var out SummarizeOutput
			if err := decodeStrict(raw, &out); err != nil {
				return nil, err
			}
			return out, nil
		},
		Demo: func() (any, error) { return DemoSummarize(in), nil },
	})
	if err != nil {
		return SummarizeOutput{}, res, err
	}
	return res.Output.(SummarizeOutput), res, nil
}

type errEmpty string

func (e errEmpty) Error() string { return string(e) + " is empty" }
