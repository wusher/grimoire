# User-interface review: compound bind

## Scope

Reviewed rich and boring output for the new CLI behavior where `bind` both
records selected skills and installs them into the active familiar.

## Findings and resolution

- **Two-stage results must remain visible.** The command preserves the binding
  page/result, then prints cast-equivalent per-skill installation results and
  the rich installation summary.
- **Avoid duplicate celebration.** Rich `bind` keeps its seal page and does not
  render the cast wand art a second time.
- **Partial failure needs an actionable state explanation.** If installation is
  blocked after binding succeeds, output states that the binding was saved and
  tells the user to fix the destination and rerun `grimoire bind`.
- **Documentation must match output.** Help and the README describe bind as an
  add-and-install operation, and the bind example includes the installation
  result and target.

Regression tests cover boring-mode partial failure guidance and rich-mode
binding page, installation result, summary, and absence of duplicate wand art.
