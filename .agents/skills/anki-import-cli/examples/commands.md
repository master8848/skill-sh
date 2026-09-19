```bash
anki-import doctor

anki-import validate examples/spanish-greetings.xml
anki-import validate examples/latex.xml
anki-import validate examples/code-and-escapes.xml
anki-import validate examples/all-note-types.xml

anki-import import examples/spanish-greetings.xml --dry-run
anki-import import examples/spanish-greetings.xml --stream
anki-import import examples/latex.xml --stream
anki-import import examples/code-and-escapes.xml --stream

anki-import checkpoint list
anki-import rollback <id> --dry-run
anki-import rollback <id>

anki-import benchmark examples/all-note-types.xml --stream
```
