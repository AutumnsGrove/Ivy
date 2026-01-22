# Project Instructions - Agent Workflows

> **Note**: This is the main orchestrator file. For detailed guides, see `AgentUsage/README.md`

---

## Project Purpose
Ivy is Grove's first-party, zero-knowledge mail client for `@grove.place` email addresses. It provides a privacy-first web interface integrated with the Grove ecosystem—users can send, receive, and organize email without Grove being able to read any of their data.

## Tech Stack
- **Language**: TypeScript
- **Frontend**: SvelteKit (SSR + client-side)
- **Backend**: Cloudflare Workers
- **Database**: Cloudflare D1 (metadata, queues)
- **Storage**: Cloudflare R2 (encrypted email bodies, attachments)
- **Auth**: Heartwood (Grove SSO)
- **Mail Server**: Forward Email (SMTP/IMAP relay)
- **Newsletters**: Postmark (broadcast streams)
- **Encryption**: AES-256-GCM (client-side), Argon2id (key derivation)
- **Package Manager**: pnpm

## Architecture Notes
### Zero-Knowledge Design
- All email content encrypted client-side before storage
- Server stores only: `user_id`, `created_at`, `r2_key`, and encrypted blobs
- Search happens client-side via IndexedDB
- Thread grouping computed client-side from email headers

### Data Flow
- **Incoming**: Forward Email webhook → D1 buffer → encrypt → R2 → delete buffer
- **Outgoing**: Compose → encrypt → D1 queue (delayed send) → Forward Email SMTP
- **Newsletters**: Compose → Postmark Broadcast Stream (Oak+ users only)

### Key Directories
```
src/
├── lib/           # Shared utilities, encryption, API clients
├── routes/        # SvelteKit routes (pages + API)
├── components/    # Svelte components
└── workers/       # Cloudflare Worker entry points
```

---

## Essential Instructions (Always Follow)

### Core Behavior
- Do what has been asked; nothing more, nothing less
- NEVER create files unless absolutely necessary for achieving your goal
- ALWAYS prefer editing existing files to creating new ones
- NEVER proactively create documentation files (*.md) or README files unless explicitly requested

### Naming Conventions
- **Directories**: Use CamelCase (e.g., `VideoProcessor`, `AudioTools`, `DataAnalysis`)
- **Date-based paths**: Use skewer-case with YYYY-MM-DD (e.g., `logs-2025-01-15`, `backup-2025-12-31`)
- **No spaces or underscores** in directory names (except date-based paths)

### TODO Management
- **Always check `TODOS.md` first** when starting a task or session
- **Check `COMPLETED.md`** for context on past decisions and implementation details
- **Update immediately** when tasks are completed, added, or changed
- **Move completed tasks** from `TODOS.md` to `COMPLETED.md` to keep the TODO list focused
- Keep both lists current and accurate

### Git Workflow Essentials

**After completing major changes, you MUST commit your work.**

**Conventional Commits Format:**
```bash
<type>: <brief description>

<optional body>

🤖 Generated with [Claude Code](https://claude.ai/code)

Co-Authored-By: [Model Name] <agent@localhost>
```

**Common Types:** `feat`, `fix`, `docs`, `refactor`, `test`, `chore`, `perf`

**Examples:**
```bash
feat: Add user authentication
fix: Correct timezone bug
docs: Update README
```

**For complete details:** See `AgentUsage/git_guide.md`

### Pull Requests

Use conventional commits format for PR titles:
```
feat: Add dark mode toggle
fix: Correct timezone bug
```

Write a brief description of what the PR does and why. No specific format required.

---

## When to Use Skills

**This project uses Claude Code Skills for specialized workflows. Invoke skills using the Skill tool when you encounter these situations:**

### Secrets & API Keys
- **When managing API keys or secrets** → Use skill: `secrets-management`
- **Before implementing secrets loading** → Use skill: `secrets-management`
- **When integrating external APIs** → Use skill: `api-integration`

### Cloudflare Development
- **When deploying to Cloudflare** → Use skill: `cloudflare-deployment`
- **Before using Cloudflare Workers, KV, R2, or D1** → Use skill: `cloudflare-deployment`
- **When setting up Cloudflare MCP server** → Use skill: `cloudflare-deployment`

### Package Management
- **When using UV package manager** → Use skill: `uv-package-manager`
- **Before creating pyproject.toml** → Use skill: `uv-package-manager`
- **When managing Python dependencies** → Use skill: `uv-package-manager`

### Authentication
- **When adding sign-in to a Grove app** → Use skill: `heartwood-auth`
- **When protecting admin routes** → Use skill: `heartwood-auth`
- **When validating user sessions** → Use skill: `heartwood-auth`
- **When integrating with Heartwood (GroveAuth)** → Use skill: `heartwood-auth`

### Version Control
- **Before making a git commit** → Use skill: `git-workflows`
- **Before creating a pull request** → Use skill: `git-workflows`
- **When initializing a new repo** → Use skill: `git-workflows`
- **For git workflow and branching** → Use skill: `git-workflows`
- **When setting up git hooks** → Use skill: `git-hooks`

### Database Management
- **When working with databases** → Use skill: `database-management`
- **Before implementing data persistence** → Use skill: `database-management`
- **For database.py template** → Use skill: `database-management`

### Research & Analysis
- **When researching technology decisions** → Use skill: `research-strategy`
- **When analyzing unfamiliar codebases** → Use skill: `research-strategy`
- **For systematic investigation** → Use skill: `research-strategy`

### Testing
- **When deciding what to test or reviewing test quality** → Use skill: `grove-testing`
- **Before writing Python tests** → Use skill: `python-testing`
- **Before writing JavaScript/TypeScript tests** → Use skill: `javascript-testing`
- **Before writing Go tests** → Use skill: `go-testing`
- **Before writing Rust tests** → Use skill: `rust-testing`

### Code Quality
- **When formatting or linting code** → Use skill: `code-quality`
- **Before major code changes** → Use skill: `code-quality`
- **For Black, Ruff, mypy usage** → Use skill: `code-quality`

### Project Setup & Infrastructure
- **When starting a new project** → Use skill: `project-scaffolding`
- **Setting up CI/CD pipelines** → Use skill: `cicd-automation`
- **When containerizing applications** → Use skill: `docker-workflows`

### Web Development
- **When building Svelte 5 applications** → Use skill: `svelte5-development`
- **For SvelteKit routing and forms** → Use skill: `svelte5-development`

### CLI Development
- **When building terminal interfaces** → Use skill: `rich-terminal-output`
- **For Rich library patterns** → Use skill: `rich-terminal-output`

### Grove UI Design
- **When creating or enhancing Grove pages** → Use skill: `grove-ui-design`
- **When adding decorative nature elements** → Use skill: `grove-ui-design`
- **When implementing glassmorphism effects** → Use skill: `grove-ui-design`
- **When working with seasonal themes** → Use skill: `grove-ui-design`
- **When building navigation patterns** → Use skill: `grove-ui-design`

### Grove Documentation
- **When writing help center articles** → Use skill: `grove-documentation`
- **When drafting specs or technical docs** → Use skill: `grove-documentation`
- **When writing user-facing text** → Use skill: `grove-documentation`
- **When writing onboarding, tooltips, or error messages** → Use skill: `grove-documentation`
- **When reviewing docs for voice consistency** → Use skill: `grove-documentation`

### Grove Specifications
- **When creating new technical specifications** → Use skill: `grove-spec-writing`
- **When reviewing specs for completeness** → Use skill: `grove-spec-writing`
- **When standardizing spec formatting** → Use skill: `grove-spec-writing`

### Museum Documentation
- **When writing documentation meant to be read by Wanderers** → Use skill: `museum-documentation`
- **When creating "how it works" content for knowledge base** → Use skill: `museum-documentation`
- **When documenting a codebase or system for curious visitors** → Use skill: `museum-documentation`
- **When writing elegant, narrative-driven technical explanations** → Use skill: `museum-documentation`

### Grove Naming
- **When naming a new service or feature** → Use skill: `walking-through-the-grove`
- **When finding a Grove-themed name** → Use skill: `walking-through-the-grove`

### Package Publishing
- **When publishing to npm** → Use skill: `npm-publish`
- **Before npm package releases** → Use skill: `npm-publish`

---

## Quick Reference

### How to Use Skills
Skills are invoked using the Skill tool. When a situation matches a skill trigger:
1. Invoke the skill by name (e.g., `skill: "secrets-management"`)
2. The skill will expand with detailed instructions
3. Follow the skill's guidance for the specific task

### Security Basics
- Store API keys in `secrets.json` (NEVER commit)
- Add `secrets.json` to `.gitignore` immediately
- Provide `secrets_template.json` for setup
- Use environment variables as fallbacks

### Available Skills Reference
| Skill | Purpose |
|-------|---------|
| `heartwood-auth` | Heartwood (GroveAuth) integration, sign-in, sessions |
| `secrets-management` | API keys, credentials, secrets.json |
| `api-integration` | External REST API integration |
| `database-management` | SQLite, database.py patterns |
| `git-workflows` | Commits, branching, conventional commits |
| `git-hooks` | Pre-commit hooks setup |
| `uv-package-manager` | Python dependencies with UV |
| `grove-testing` | Testing philosophy, what/when to test |
| `python-testing` | pytest, fixtures, mocking |
| `javascript-testing` | Vitest/Jest testing |
| `go-testing` | Go testing patterns |
| `rust-testing` | Cargo test patterns |
| `code-quality` | Black, Ruff, mypy |
| `project-scaffolding` | New project setup |
| `cicd-automation` | GitHub Actions workflows |
| `docker-workflows` | Containerization |
| `cloudflare-deployment` | Workers, KV, R2, D1 |
| `svelte5-development` | Svelte 5 with runes |
| `rich-terminal-output` | Terminal UI with Rich |
| `grove-ui-design` | Glassmorphism, seasons, forests, warm UI |
| `grove-documentation` | Grove voice, help articles, user-facing text |
| `grove-spec-writing` | Technical specifications with Grove formatting |
| `museum-documentation` | Elegant, narrative documentation for Wanderers |
| `walking-through-the-grove` | Finding Grove-themed names for new services |
| `npm-publish` | npm package publishing workflow |
| `research-strategy` | Systematic research |

---

## Code Style Guidelines

### Function & Variable Naming
- Use meaningful, descriptive names
- Keep functions small and focused on single responsibilities
- Add docstrings to functions and classes

### Error Handling
- Use try/except blocks gracefully
- Provide helpful error messages
- Never let errors fail silently

### File Organization
- Group related functionality into modules
- Use consistent import ordering:
  1. Standard library
  2. Third-party packages
  3. Local imports
- Keep configuration separate from logic

---

## Communication Style
- Be concise but thorough
- Explain reasoning for significant decisions
- Ask for clarification when requirements are ambiguous
- Proactively suggest improvements when appropriate

---

## Additional Resources

### Skills Documentation
Skills are the primary way to access specialized knowledge. Use the Skill tool to invoke them.
Skills are located in `.claude/skills/` and provide concise, actionable guidance.

### Extended Documentation
For in-depth reference beyond what skills provide, see:
**`AgentUsage/README.md`** - Master index of detailed documentation

---

*Last updated: 2026-01-22*
*Model: Claude Opus 4.5*
