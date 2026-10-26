# Browser dependencies

These files are served locally; no CDN is contacted by the application at runtime.

| Library | Version | Download source | SHA-256 |
| --- | --- | --- | --- |
| Marked | 18.1.1 | https://cdn.jsdelivr.net/npm/marked@18.1.1/lib/marked.umd.js | `b82b9334406d8bdd16b6236c08de1cb65b2a859873ca9503d9cc0b292ca3c12a` |
| DOMPurify | 3.4.17 | https://raw.githubusercontent.com/cure53/DOMPurify/3.4.17/dist/purify.min.js | `cd37898f9782adf54820c413191b5691803f0f137c799d6edcfd38523b4c2c31` |

Marked's MIT license is in `marked-LICENSE`; DOMPurify's Apache-2.0 OR MPL-2.0 license is in `DOMPurify-LICENSE`. Release sources: https://github.com/markedjs/marked/releases/tag/v18.1.1 and https://github.com/cure53/DOMPurify/releases/tag/3.4.17.

Marked parses Markdown; it does not sanitize HTML. The application passes parsed HTML to DOMPurify with a restricted allowlist before inserting a DOM fragment. Missing or failed dependencies fall back to plain text.
