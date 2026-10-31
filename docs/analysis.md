# Review analyses

A review analysis regroups a pull request's changes for a reader. A git diff is ordered by file and hunk because that is how diff works, not because that is how code is understood; an analysis arranges the same changes along axes that help a reviewer.

- **Lenses** are ways of looking at the pull request ("General Flow", "Claim Justifications", …). A lens need not cover the whole pull request.
- Each lens holds **segments**: sets of spans read together, with a title, an optional risk (`high`, `medium`, `low`), an annotation about the whole set, per-span notes, and a reading order.
- **Spans** (called `hunks` in the file) are runs of adjacent lines of one file, defined once at the top level and referenced by id, so one span can appear in several segments and lenses, annotated differently in each. A span may cover several git hunks, or unchanged code (`context_only`).
- **Edges** relate spans: `calls`, `tests`, `documents`, … (an open vocabulary).

Line numbers on the `RIGHT` side are against `pr.head_sha`; on the `LEFT` side against `pr.merge_base` (the commit the pull request's diff is computed from, not `base_sha`).

## Loading one

- When a pull request opens, strig loads `.strig/analysis/<number>.json` relative to where it runs, if the file exists.
- `:analysis <path>` loads any file; `:analysis` alone loads the conventional one again.
- An analysis for another repository or pull request is refused. One made for another head than the diff on screen loads, marked stale: its line numbers may have moved.

## Reading through a lens

`:lens` shows the first lens in place of the Files list, `gt` / `gT` move to the next / previous lens, `:lens <id>` opens a given one, and `:lens` again closes it. The pane lists the lens's segments in the lens's `order` (then any it leaves out), each with its title, risk and annotation, and under each its spans in the segment's `order` (then the rest). A span used by two segments appears under both.

`j`/`k` (and `gg`/`G`) move between spans; the diff pane follows to the span's file and puts its first line at the top of the pane, on the old side for `LEFT` spans. The span's lines are marked with a bar in the diff's gutter, from its first line in the diff to its last. `enter` moves focus to the diff; `:next-span` and `:previous-span` work from there. A span outside every hunk is marked "not in the diff" (the diff goes to the closest line), one in a file the pull request does not change "file not changed" (the diff stays where it is).

## Format

The format is `prreview/analysis@1`. strig refuses unknown fields and checks that every reference resolves (segment spans, both `order` lists, `notes` keys, edge ends), reporting each problem with where it is. The JSON Schema:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "prreview/analysis-1.schema.json",
  "title": "PR review analysis",
  "description": "A PR regrouped for human review. A git diff is ordered by file and hunk because that is how diff works, not because that is how code is understood. This describes the same changes arranged along whatever axes actually help a reviewer: lenses hold segments, segments hold hunks, hunks are spans of lines. Hunks are defined once at the top level and referenced by id, so the same span can appear in several segments and several lenses, annotated differently in each. Overlap is the point, not a defect.",
  "type": "object",
  "required": ["schema_version", "pr", "hunks", "lenses"],
  "additionalProperties": false,
  "properties": {
    "schema_version": { "const": "prreview/analysis@1" },
    "generated_at": { "type": "string" },
    "generator": { "type": "string" },

    "pr": {
      "type": "object",
      "required": ["repo", "number", "head_sha", "merge_base"],
      "additionalProperties": false,
      "properties": {
        "repo": { "type": "string" },
        "number": { "type": "integer" },
        "title": { "type": "string" },
        "head_sha": { "type": "string", "description": "RIGHT-side line numbers are against this commit" },
        "base_sha": { "type": "string" },
        "merge_base": { "type": "string", "description": "LEFT-side line numbers are against THIS, not base_sha — the patch is computed from the merge base, and they differ whenever the base branch has moved" }
      }
    },

    "hunks": {
      "type": "object",
      "description": "Logical spans, not git hunks. A span is a run of adjacent lines chosen because they read together; it may cover several literal git hunks, and it may include unchanged lines — context the reviewer needs, or code that was not touched at all but must be looked at (an unchanged caller, say).",
      "propertyNames": { "pattern": "^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$" },
      "additionalProperties": {
        "type": "object",
        "required": ["path", "side", "start", "end"],
        "additionalProperties": false,
        "properties": {
          "path": { "type": "string" },
          "side": { "enum": ["RIGHT", "LEFT"],
                    "description": "RIGHT: line numbers in the head commit. LEFT: line numbers in the merge base, for spans that only exist before the change." },
          "start": { "type": "integer", "minimum": 1 },
          "end": { "type": "integer", "minimum": 1 },
          "label": { "type": "string", "description": "A few words naming the span, independent of any lens." },
          "context_only": { "type": "boolean", "default": false,
                            "description": "True when the span contains no changed lines — it is here to orient the reader, and tooling may style or fold it differently." }
        }
      }
    },

    "edges": {
      "type": "array",
      "description": "Directed relationships between spans, so a reviewer can be walked from a change to the thing it affects rather than to the next filename alphabetically. Global rather than per-lens: an edge states a fact about the code, and a lens decides whether to draw it.",
      "items": {
        "type": "object",
        "required": ["from", "to", "kind"],
        "additionalProperties": false,
        "properties": {
          "from": { "type": "string" },
          "to": { "type": "string" },
          "kind": { "type": "string",
                    "description": "Open vocabulary. Useful ones so far: calls, called-by, delegates-to, implements, tests, documents, contract-depends-on, renamed-from, moved-from, supersedes." },
          "note": { "type": "string" }
        }
      }
    },

    "lenses": {
      "type": "array",
      "minItems": 1,
      "description": "Ways of looking at the PR. Deliberately not enumerated: which lenses are useful depends on the reviewer and the change. Examples seen in practice: General Flow (follow the call path), Documentation, Comment Evaluation, Claim Justifications (what does this PR assert, and what backs it), Behavioural vs Inert. A lens need not cover the whole PR; the analysis as a whole should.",
      "items": {
        "type": "object",
        "required": ["id", "title", "segments"],
        "additionalProperties": false,
        "properties": {
          "id": { "type": "string", "pattern": "^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$" },
          "title": { "type": "string" },
          "purpose": { "type": "string", "description": "What question this lens helps a reviewer answer." },
          "order": { "type": "array", "items": { "type": "string" },
                     "description": "Segment ids, in the order they should be read." },
          "segments": {
            "type": "array",
            "minItems": 1,
            "items": {
              "type": "object",
              "required": ["id", "title", "hunks"],
              "additionalProperties": false,
              "properties": {
                "id": { "type": "string", "pattern": "^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$" },
                "title": { "type": "string" },
                "risk": { "enum": ["high", "medium", "low"] },
                "annotation": { "type": "string",
                                "description": "Free text about the SET of spans: what it does, and what a reviewer should check." },
                "hunks": { "type": "array", "minItems": 1, "items": { "type": "string" },
                           "description": "Hunk ids. A span may be claimed by any number of segments — a renamed line inside a restructured block belongs to both the rename and the restructure, and forcing a choice would be a worse segmentation." },
                "notes": {
                  "type": "object",
                  "description": "Free text about ONE span, specific to this segment. The same span can read differently under a different lens, so the note lives here rather than on the hunk.",
                  "additionalProperties": { "type": "string" }
                },
                "order": { "type": "array", "items": { "type": "string" },
                           "description": "Hunk ids, in the order they should be read within this segment." }
              }
            }
          }
        }
      }
    }
  }
}
```
