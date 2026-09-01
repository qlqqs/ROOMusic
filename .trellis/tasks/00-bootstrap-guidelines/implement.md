# Spec Bootstrap Implementation Plan

1. Add shared product-goal, modular-design, and engineering-principle guides;
   source product intent from `ROOMusic-V0/.planning/PROJECT.md` and related
   planning documents, and record Core 0 supersessions explicitly;
   tailor the reuse and cross-layer guides to ROOMusic and update their index.
2. Rewrite backend index and five backend guidelines with Core 0 contracts,
   module ownership, persistence boundaries, errors, logging, and quality gates.
3. Rewrite frontend index and six frontend guidelines with feature ownership,
   typed REST access, state separation, component contracts, accessibility, and
   quality gates.
4. Add source references and short examples from the current repository and the
   Core 0 planning artifacts; label unimplemented conventions as contracts.
5. Remove all generated placeholder headings/prose and synchronize all index
   files with the final set of documents.
6. Run verification:
   - `grep -R "To be filled\\|TODO: fill\\|Replace with your actual" .trellis/spec`
   - link/path checks for every index entry;
   - `git diff --check` (including untracked spec files via direct inspection);
   - consistency search for forbidden Core 0 claims.
7. Review the complete spec tree for product-scope consistency,
   high-cohesion/low-coupling rules, smallest-complete-change discipline,
   focused-function guidance, and reuse without premature abstraction. Ensure no
   document asserts a library choice that the project has not selected.

## Rollback Point

All changes are documentation-only and scoped to `.trellis/spec/` plus these
planning artifacts. If verification finds a contradiction, revise the affected
document before any product implementation begins; do not weaken the Core 0 PRD
to fit a generated template.
