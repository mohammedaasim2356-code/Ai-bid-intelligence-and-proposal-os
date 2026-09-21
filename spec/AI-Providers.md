# AI-Providers.md — Free-Tier AI Router (v2)

## 1. Why this file exists

v1 made the product runnable without paid AI, but the only AI in the free path was a
template engine (`DemoAiProvider`). A prospect watching the demo saw canned text, and a
real uploaded RFP could not be extracted or answered. v2 keeps the deterministic demo as
the floor and adds a **real-AI path that costs nothing**, built from local models and
provider free tiers, routed through one adapter.

Rule: free/trial access is a moving target. Never hard-code quotas, model names, or
limits in application code. Everything below is configuration.

## 2. Provider chain

```text
AiRouter
├── LocalProvider        (Ollama / llama.cpp server, OpenAI-compatible, $0, private)
├── FreeTierProvider[]   (any OpenAI-compatible endpoint with a free tier or trial key,
│                         e.g. Google Gemini API, Groq, OpenRouter free models,
│                         Cloudflare Workers AI — configured, never assumed)
├── PaidProvider         (optional; only if the user supplies a key)
└── DemoAiProvider       (deterministic floor; always available)
```

All remote providers share one `OpenAICompatibleProvider` implementation plus
per-provider config. Anything that is not OpenAI-compatible gets its own thin adapter.

## 3. Router interface

```ts
interface AiRouter {
  run<TOut>(task: AiTask<TOut>): Promise<AiResult<TOut>>;
}

interface AiTask<TOut> {
  kind: 'extract' | 'classify' | 'draft' | 'verify' | 'summarize' | 'sme_question';
  promptId: string;            // versioned prompt module, e.g. "draft.answer@3"
  input: unknown;              // validated by the prompt's input schema
  outputSchema: ZodType<TOut>;
  sensitivity: 'synthetic' | 'internal' | 'confidential';
  maxTokens: number;
}

interface AiResult<TOut> {
  output: TOut;
  providerId: string;
  model: string;
  mode: 'demo' | 'local' | 'free_tier' | 'paid';
  cached: boolean;
  attempts: number;
  latencyMs: number;
}
```

## 4. Routing policy (in order)

1. **Sensitivity gate.** `confidential` tasks may only use providers whose config has
   `allowConfidential: true` (default: local only). Some free tiers permit the provider
   to use submitted data to improve its services; the admin must opt each provider in
   after reading its current terms. Synthetic demo data may go anywhere.
2. **Cache.** Key = `sha256(promptId + model + canonicalJSON(input))`. Hit → return.
   This makes re-runs, demos and evals nearly free.
3. **Budget + rate guard.** Each provider has a token bucket (requests/min, tokens/day)
   read from config. If a call would exceed it, skip to the next provider instead of
   failing.
4. **Call + validate.** Parse against `outputSchema`. On schema failure, one repair
   attempt (send the validation error back). Second failure → next provider.
5. **Transient errors** (429, 5xx, timeout): exponential backoff once, then next provider.
   Deterministic errors (400, schema violation twice): do not retry on the same provider.
6. **Floor.** If every provider is unavailable, `DemoAiProvider` answers and the UI shows
   `SYNTHETIC RESULT`. The product never hard-fails because a trial expired.

## 5. Configuration

`ai.providers.json` (or env), validated at boot, surfaced read-only on
Settings → AI Providers with live status (Available / Connected / Not configured /
Rate-limited / Error):

```json
{
  "order": ["local", "freeA", "freeB", "demo"],
  "providers": {
    "local": { "type": "openai_compatible", "baseUrl": "http://localhost:11434/v1",
               "model": "${LOCAL_MODEL}", "allowConfidential": true },
    "freeA": { "type": "openai_compatible", "baseUrl": "${FREE_A_BASE_URL}",
               "apiKeyEnv": "FREE_A_API_KEY", "model": "${FREE_A_MODEL}",
               "rpm": "${FREE_A_RPM}", "tokensPerDay": "${FREE_A_TPD}",
               "allowConfidential": false }
  },
  "taskOverrides": { "verify": ["local", "demo"] }
}
```

Tip for the coding agent: cheap/fast models for `classify` and `verify`, the strongest
available model for `draft` and `extract`.

## 6. Embeddings (free, local)

- Default: an in-process sentence-embedding model via Transformers.js (ONNX, CPU).
  Pick a small general-purpose English model; record the choice in `Memory.md`.
- Store vectors as `Float32Array` blobs on `DocumentChunk`. Brute-force cosine is fine
  below roughly 50k chunks; do not add a vector database for V1.
- Optional adapter: remote embeddings endpoint, same sensitivity gate.
- Re-embed only when `chunk.textHash` or `embeddingModelId` changes.

## 7. Observability (ProviderCall table)

Log per call: task kind, promptId, provider, model, mode, cached, attempts, latency,
input/output token counts, outcome, error class. Never log prompt content for
`internal`/`confidential` tasks unless `DEBUG_PROMPTS=true` in a local environment.

Settings page shows: calls today per provider, cache hit rate, fallbacks triggered,
schema-repair rate. This doubles as a sales-demo asset ("here is exactly what the AI did").
