# Reuse before writing

Search for an existing implementation before adding one. Grep the repository for the
capability and extend the code that is already there. Two implementations of one behavior
are a defect, not redundancy: they drift, and the second one stops matching the first.

Every defect found in the engine while working in any repository is reported as an issue in
the upstream repository. A fleet repository that works around an engine defect locally
leaves the engine broken for every other adopter.
