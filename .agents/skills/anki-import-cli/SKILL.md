---
name: anki-import
description: Manage Anki flashcards as code via the anki-import.Any thing anki.
---
anki-import CLI

The main way to manage Anki cards as code. Simple, safe, scriptable.

Install: `npm i -g anki-xml` → you get `anki-import` and `anki-xml`. Needs Node or bun 

## Quick start

```bash
anki-import open                  # open Anki (if not already open)
anki-import doctor                # check Anki is reachable
anki-import validate cards.xml    # check file — no Anki needed
anki-import plan cards.xml        # preview what will change
anki-import import cards.xml      # add new cards
anki-import sync cards.xml        # add + update cards
anki-import rollback <id>         # undo last import
```
## Commands

| Command | What it does |
|---|---|
| `open` | Open Anki app |
| `doctor` | Check Anki connection |
| `validate <file>` | Check file for errors |
| `plan <file>` | Preview changes |
| `diff <file>` | Show what changed |
| `import <file>` | Add new cards |
| `sync [<file>]` | Add + update, or show missing if no file |
| `rollback <id>` | Undo an import |
| `watch <file>` | Watch file and auto-apply |
| `anki-sync` | Check AnkiWeb auth + sync, trigger sync (`--check` / `--json`) |
| `tags` / `models` / `stats` / `media` | Manage tags, types, stats, files |
| `mcp` | Start MCP server (for AI agents) |

Flags:  `--dry-run`, `--deck NAME`, `--model NAME`, `--batch-size N`, `--yes`, `--url http://127.0.0.1:8765`.

## How to write cards

XML is the main format. YAML, JSON, CSV, Markdown all work — they become the same card.

```xml
<anki deck="Spanish">
  <note type="Basic">
    <field name="Front">Hola</field>
    <field name="Back">Hello</field>
  </note>
</anki>
```

- Root is `<anki deck="...">`, each card is `<note type="..." deck="..." tags="...">`
- Every card needs a deck

### Cloze cards

Cloze uses `<text>` (must contain `{{cN::...}}`) and optional `<extra>`:

```xml
<anki deck="Biology">
  <note type="Cloze">
    <text>The {{c1::mitochondrion}} is the powerhouse of the {{c2::cell}}.</text>
    <extra>High-yield fact.</extra>
  </note>
</anki>
```

- `{{c1::answer}}` → card 1, `{{c2::answer}}` → card 2; reuse `c1` to hide multiple spots on same card
- Hint: `{{c1::Paris::largest city}}`
- `{{c1,2::shared}}` validates but Anki 25.09 won't create cards — stick to single ordinals for now
- Missing marker fails `validate`: `<text> must contain {{cN::...}}`

See `examples/all-note-types.xml` (Cloze example) and `examples/code-and-escapes.xml`.

### Formatting: when to use CDATA

Keep it simple:


1. Real formatting — no CDATA 
<field name="Back">Line one<br>Line two</field>
<field name="Back"><b>Bold</b> and <u>underline</u></field>

2. Show code as text — use CDATA  
<field name="Front"><![CDATA[What does <div> do?]]></field>
<field name="Back"><![CDATA[Use <div class="box">Hi</div>]]></field>
Important : Do not cdata unless creating node for learning html or php or need to show user literal <div> and <br> insted of styling
Tip: using &lt;br&gt; is better if styling mix is required

3. Mix — split CDATA
<field name="Text"><![CDATA[Code: ]]><b><![CDATA[{{c1::answer}}]]></b><![CDATA[ here]]></field>

Check: `anki-import validate file.xml` — bare `&` must be `&amp;`.

### AnkiWeb sync — will it reach your phone?

Cards are local until Anki syncs to AnkiWeb. Phone gets them after Anki's own sync.


anki-import anki-sync              # check auth + trigger AnkiWeb sync
anki-import anki-sync --check      # only check (no sync)


- If not logged in: `Not authenticated — log into AnkiWeb in Anki: Tools → Preferences → Syncing → Log in, then retry.` (exit 1, `cause: "auth"`)
- If Anki not open: same hints as `doctor` (`cause: "refused"` etc.)
- After `import`/`sync <file>`, run `anki-sync` then sync on phone to verify.

Examples: `all-note-types.xml`, `code-and-escapes.xml`, `latex.xml`, `spanish-greetings.xml` (in `skills/anki-import-cli/examples/`).


anki-import import examples/basic.xml --dry-run --json
