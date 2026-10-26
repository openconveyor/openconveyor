# STE style for Vale

These rules check the mechanical parts of `docs/writing-style.md`
(Simplified Technical English). Every rule is a warning or a
suggestion, so `vale` exits 0.

| Rule | Type | Source |
|---|---|---|
| `SentenceLength` | occurrence | this repository |
| `ParagraphLength` | occurrence | this repository |
| `Passive` | existence | `errata-ai/write-good` v0.4.1, MIT License, Copyright (c) 2019 errata.ai |
| `Substitution` | substitution | the word list in `docs/writing-style.md` |
| `Latin` | existence | this repository |
| `Hedging` | existence | this repository |
| `FutureTense` | existence | this repository |

Keep `Substitution.yml` and the word list in the guide in step.
