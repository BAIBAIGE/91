# Documentation Rules

- Do not modify the README.md file in the project root directory unless explicitly requested.

# Engineering Principles

## Code Quality

- Write code that is clear, understandable, and easy to maintain.
- Prefer simple, consistent, and well-structured solutions over clever or overly complex implementations.
- Code should communicate intent clearly. Avoid unnecessary abstractions, indirection, or hidden behavior.

## Cohesion and Coupling

- Prefer high cohesion and low coupling in system design.
- The goal is not to eliminate coupling completely. A certain level of coupling is necessary for components to collaborate effectively.
- Aim for intentional, explicit, and manageable coupling:
  - Dependencies between components should represent meaningful relationships.
  - Dependencies should be visible and easy to understand.
  - Prefer well-defined interfaces and abstractions over implicit assumptions or hidden dependencies.
  - Avoid unnecessary knowledge sharing between modules.
  - Keep responsibilities clearly separated so changes remain localized.

- When introducing or modifying dependencies, consider:
  - Is this dependency required for a meaningful collaboration, or is it accidental coupling?
  - Is the dependency direction appropriate?
  - Does this relationship belong behind an abstraction?
  - Will this design remain understandable and maintainable as the system grows?

## Architecture and Design

- Consider system-level impact when making changes.
- Avoid solving problems only at the local code level when the issue is caused by architectural problems.
- Prefer designs that improve long-term reliability, maintainability, and extensibility.
- Avoid introducing special cases, workarounds, or hacks unless there is a clear architectural justification.

# Bug Fixing Principles

- When a bug is discovered, do not immediately apply a temporary patch.
- First identify the root cause of the problem.
- Analyze the issue from a system perspective:
  - Is the current architecture contributing to the problem?
  - Is there a missing abstraction or incorrect responsibility boundary?
  - Does another component need redesign or optimization?
- Fix the underlying cause instead of only addressing the observed symptom.

# Compatibility Principles

- Backward compatibility is not required by default.
- Prioritize correctness, maintainability, and architectural consistency over preserving legacy behavior.
- Breaking changes are acceptable when they improve the overall system design or resolve fundamental issues.

# Before Making Changes

Before modifying code:

1. Understand the existing architecture and design intent.
2. Identify the affected components and their responsibilities.
3. Explain the root cause of the problem.
4. Evaluate whether the issue indicates a broader design problem.
5. Consider alternative solutions and choose the one with the best long-term maintainability.
6. Only then implement the change.

# Git Commit Conventions

When creating Git commits in this repository:

- Write commit messages in English.
- Use an imperative, sentence-case subject.
- Describe the outcome of the change, not the development activity.
- Do not use Conventional Commit prefixes such as `feat:`, `fix:`, or `chore:`.
- Keep the subject at 75 characters or fewer and do not end it with a period.
- Keep each commit focused on one conceptual change.
- Use backticks around code identifiers when helpful.
- For non-trivial commits, structure the body with:
  - `## Why` to explain the problem or motivation.
  - `## What changed` to summarize the implementation.
  - `## Testing` to list actual tests and validation performed.
- Omit `## Why` when the motivation is obvious.
- If tests were not run, state that explicitly and explain why.
- Only create a Git commit when the user explicitly asks for one.

Example subject:

`Expose thread originators through the app-server API`