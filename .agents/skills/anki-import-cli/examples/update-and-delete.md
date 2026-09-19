```bash
# Delete notes from the last import
anki-import checkpoint list
anki-import rollback <id> --dry-run
anki-import rollback <id>

# Delete specific note ids
anki-import checkpoint create delete-batch --note-ids 111,222,333
anki-import rollback delete-batch --dry-run
anki-import rollback delete-batch

# Update = delete old notes, then import corrected XML
# (no update command in this release)
anki-import checkpoint create replace-batch --note-ids 111,222
anki-import rollback replace-batch
anki-import import corrected.xml --stream
```
