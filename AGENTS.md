# Aurora Agentic System

## Project Structure

- `doc/design`: design documents. Use them to understand system design ideas, architecture, and development specs.
  - Document guide:
    - `Flory-JIT.md`: dynamic JIT graph-expansion design. For Steps that cannot map directly to an existing Skill, the LLM is invoked again to plan a child graph.
    - `Intent-Router.md`: Intent Router design. It describes how Flory analyzes user prompts, injects Skills, and decomposes requests into DAGs.
    - `System-Spec.md`: protocol specs for module interactions.
    - `Aurora-Architechure.md`: overall system architecture. Refer to it for development planning, progress review, and architecture review.
    - `Plato-GraphRAG.md`: detailed design for the GraphRAG subsystem. Refer to it when designing GraphRAG-related features.
    - `Mem3.md`: detailed design for memory system. Refer to it when designing and developing memory system for agents.
- `doc/progress`: development progress records. Review these during implementation. Whenever development makes meaningful progress, update the relevant progress document, such as removing completed TODOs to avoid duplicate future work.
- `doc/review`: output location for architecture and code review reports.
  - `logs`: regular code and architecture review records.
- `doc/dev`: development and debugging guides, including usage notes for test tools.
- `doc/spec`: concrete development protocol specs, such as DAG generation constraints and Sandbox/Skill interaction protocols. If a review or design proposal involves protocols, place the output here.
- `doc/plan`: concrete R&D plans for architecture design and optimization proposals. Update the relevant plan document when reviewing architecture, drafting a new proposal, or adjusting development priorities.
  - `Phase-Plan.md`: overall project R&D plan. Every plan adjustment must be recorded in the overall R&D plan.
- `doc/optimization`: optimization proposals and technical implementation details.
- `doc/refactor`: refactoring documentation.

Refer to `README.md` for concrete project directories and feature descriptions.

When generating documentation, write it to the category-specific directory.

## Plan

- In the first phase, implement only the core framework. Some LLM call interfaces may initially implement the flow with mocked data. The TS Worker can provide a few simple demos and does not need to simulate a serverless function-computing environment. However, core DAG transitions, scheduler logic, and memory management, including GraphRAG, rolling compression, and internal Skills that query KvRocks output, must implement their core logic.
- Tests in this phase may mock unfinished components or implement the smallest testable units.
- In the second phase, refine the system and replace mocked components from the previous phase one by one.
- In the third phase, try local deployment while supporting both local and cloud-native services.

## Development Environment

- The initial development environment should make it easy to run a demo on a local macOS M2 machine. Component dependencies can initially be provided through Docker Compose as a minimal viable setup. Prioritize convenient local development and debugging.
- The local development environment includes IDEs such as GoLand and VSCode. The project should support visual breakpoint debugging in these IDEs. When generating project scaffolding, include the VSCode configuration required for Rust, Go, TypeScript, and related language environments.

## Spec

- During project development, generate the necessary editor configuration, language-specific lint configuration, and formatting configuration. Follow Google Style strictly for code style checks and formatting.
- Git commits must follow conventional commit standards, referencing conventions used by large open-source projects.
- Development uses the `main` branch by default. For remote repository setup, refer to:

```shell
git remote add origin git@github.com:linuxb/Aurora.git
git branch -M main
git push -u origin main
```

- After a module unit is complete and has corresponding tests, run the unit tests and then create a small module-level commit.

## Notice

- When developing features, first check whether they can be implemented with mature mainstream libraries, such as libraries for SQL operations, HTTP/network basics, encoding, and decoding. Avoid spending time reinventing low-value wheels.
