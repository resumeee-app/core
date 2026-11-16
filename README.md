<p align="center">
  <img src="./.github/logo-192x192.png" alt="Resumeee" width="96" />
</p>

# core

Go domain library for **Resumeee** (`github.com/resumeee-app/core`).

## What It Contains

- **Career Graph & Profile Services** — Core resume entities, revision tracking, and JSON Resume import/export.
- **Branching & Versioning** — Role-targeted resume branches, immutable versions, and built-in resume templates.
- **Provenance & PII Protection** — Grounding validation to prevent fabricated claims, plus PII obfuscation across LLM calls.
- **LLM & PDF Processing** — Multi-provider LLM clients (Gemini, OpenAI, Anthropic, Ollama, OpenAI-compatible), PDF text extraction, and background job workers.
- **Template Rendering & In-Memory Store** — HTML, Markdown, and Plain Text rendering along with an in-memory store for testing and embedded use.

## Verification

Requires **Go 1.27+**.

```bash
make verify
```

---

## License & Trademarks

### License
Resumeee is licensed under the **GNU Affero General Public License v3.0 or later** ([AGPL-3.0-or-later](LICENSE)).

You are free to use, modify, and host this software. Under the AGPL, any modified versions distributed or hosted over a network must make their complete source code freely available under the same license terms.

For commercial inquiries or custom closed-source licensing arrangements, contact Ruben Rivera.

### Contributor Agreement
All external contributions are welcomed under the terms of our [Contributor License Agreement (CLA)](CLA.md). See [CONTRIBUTING.md](CONTRIBUTING.md) for details.

### Trademark Notice
"Resumeee" and the Resumeee branding and logo are trademarks of Ruben Rivera. The AGPLv3 license gives you rights to the software code, but does not grant rights to use the official name, logo, or trademarks for derivative products or publicity without prior written permission.
