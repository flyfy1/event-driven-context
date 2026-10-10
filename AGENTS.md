# Repository Guidance for Agents

## Documentation Language

- English is the default language for repository documentation and new developer-facing prose unless the user explicitly requests another language.
- Canonical `*.md` documents are English. Their Simplified Chinese translations use the `*.cn.md` suffix.
- `README.md` is the canonical English README displayed by default on GitHub; `README.cn.md` is its Simplified Chinese translation. Do not recreate `README-en.md` or introduce a second Chinese suffix.
- Keep the language switcher at the top of both README files.
- Required bilingual coverage: the root README, and every user-facing document under `docs/` (product scope, UX flows, architecture, Hub features and setup, CLI and plugin guides, production deployment, proposals). A new user-facing document gets its `*.cn.md` pair in the same change. A document that already has a `*.cn.md` pair is bilingual, whatever its category.
- Internal engineering notes, implementation and acceptance plans, research, module READMEs, operational setup notes, `SKILL.md` files, and agent prompts are English-only unless a translation pair already exists. This includes unpaired `docs/notes-*.md` and `docs/v2-*.md` files, `backend/README.md`, `app/README.md`, and `deploy/production/proxy-setup.md`. Existing localized agent prompts retain their own filenames; they do not require additional `*.cn.md` pairs.
- Do not infer a translation requirement for every Markdown file or create missing translations solely to satisfy this policy. Preserve and synchronize any existing English/Chinese pair, including internal or archived documents.
- When changing product facts, commands, setup steps, limitations, or architecture in a bilingual document, update its English and Chinese files in the same change so the two versions stay equivalent.
- Code identifiers, API paths, flags, filenames, and commands must remain exact across translations.

## Product Principles

- Event-driven Context is local first, open source, and self-hostable. Lead with deployer control of persisted data and access permissions.
- Distinguish persistence location from model-call data flow. Self-hosting does not imply that content sent to an external model remains on the device.
- Core recording, reading, and deterministic querying must not depend on a cloud model.
- Events, original files, and metadata are append-only through the product interfaces. Preserve provenance and explicit relationships for processed outputs.
- Do not present the maintainers' `integ.life` deployment as a prerequisite for self-hosting.

## Development Stage

This project is in the MCVP stage:

- Do not preserve backward compatibility by default or add compatibility layers solely for older versions.
- Choose the best architecture, API, data format, and configuration for the current requirements and verified runtime conditions.
- Refactor existing designs when needed within the user-authorized task scope.
- Update relevant documentation and verification together with implementation changes.

## Working and Verification Rules

- Preserve unrelated worktree changes and stage only files owned by the current task.
- Use explicit module working directories such as `backend/`, `frontend/`, and `app/`; do not assume root-level Go or npm commands are valid.
- Run `make check` and `make build` for repository-wide changes when applicable. For documentation-only changes, at minimum run `git diff --check` and validate links, headings, and language parity.
- A build or local check does not prove production behavior. For release work, verify the actual public route, service identity, and user flow.
- After each independently verifiable repository-work stage, commit the task-owned files and push the intended branch without including unrelated changes.
