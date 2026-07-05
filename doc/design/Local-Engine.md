# **Aurora Local-First and Sandbox Adapter Architecture Whitepaper**

As the Local AI ecosystem grows and privacy-computing demand rises, Aurora must support both cloud-scale high concurrency and a reduced local-first Agent mode running on a user's Unix/macOS machine.

This document describes Aurora's cross-environment Infra Adapter Layer and the Zig-based local TypeScript high-speed secure sandbox service.

## **1. Infra Adapter Layer Design**

To achieve "one business codebase, smooth dual-mode execution," Aurora introduces the Adapter pattern in Flory, the scheduling brain, and Mem3, the memory engine. Through dependency injection, startup loads different storage drivers according to an environment scalar such as `ENV=cloud` or `ENV=local`.

### **1.1 Core Interfaces**

In the Go scheduling gateway, concrete database implementations are separated from three core data-plane interfaces:

```go
// 1. State Engine interface.
type ITaskStateStore interface {
    FetchReadyTasks(batchSize int) ([]Task, error) // Corresponds to SKIP LOCKED behavior.
    AtomicDecrementDependency(taskID string) (int, error) // Corresponds to dependency decrement.
    BeginTx() (Transaction, error) // Transaction required by JIT graph expansion.
}

// 2. Context/Memory Store interface.
type IContextStore interface {
    PutTaskRawData(taskID string, payload []byte) error
    GetTaskRawData(taskID string) ([]byte, error)
}

// 3. Graph Engine interface.
type IGraphStore interface {
    UpsertRelation(edge Relation) error
    QuerySubgraph(query GraphQuery) ([]Relation, error)
}
```

### **1.2 Cloud and Local Driver Mapping Matrix**

| Interface responsibility | Cloud-native driver | Local-first driver | Local design consideration |
| --- | --- | --- | --- |
| **State control** (`ITaskStateStore`) | **TiDB** distributed SQL | **SQLite** embedded SQL | SQLite is cross-platform and supports fast local ACID transactions. `BEGIN EXCLUSIVE` or WAL can approximate Flory's local scheduling state flow without requiring users to install a database process. |
| **Raw context** (`IContextStore`) | **Apache KvRocks** | **Local File System (Markdown) / BadgerDB** | In local mode, task `raw_data` can be serialized as Markdown files such as `workspace/memory/task_123.md`. This keeps Agent records readable in a text editor and improves transparency. |
| **Graph memory** (`IGraphStore`) | **NebulaGraph / Neo4j** | **KuzuDB / DuckDB** | Avoid heavy graph database services. KuzuDB is an embedded graph library, while DuckDB can validate edge-table modeling in the first local phase. |

### **1.3 Cross-Platform Compatibility (Unix & macOS)**

- **Pure Go compilation**: the Flory scheduler is written in Go and can be cross-compiled into one static binary for macOS Apple Silicon, macOS Intel, and Linux.
- **CGO constraints**: for SQLite and KuzuDB, prefer pure-Go implementations where possible, such as `modernc.org/sqlite`, to avoid cross-environment compilation complexity.

## **2. Zig-Driven TS Secure Sandbox (Aegis Sandbox)**

### **2.1 Critical Challenges for Local Skill Execution**

In the cloud, TS Skills run in disposable Docker containers. In local mode:

1. **Deep environment dependencies**: we cannot assume users have Node.js or Bun installed.
2. **High security risk**: malicious TS Skills downloaded by an Agent could damage the host if executed directly.
3. **Cold-start sensitivity**: local machines cannot afford frequent startup of heavy virtual machines.

### **2.2 Solution: Aegis Embedded Engine Built with Zig**

Using Zig's strong C interoperability, explicit control flow, and microsecond-level startup, Aurora builds **Aegis** as an independent sandbox daemon with an embedded **QuickJS** engine.

#### **2.2.1 Architecture Workflow**

1. **Dispatch**: Flory (Go) sends precompiled JS code and execution parameters to Aegis (Zig) through Unix Domain Socket or STDIN.
2. **Initialize**: Zig creates a new QuickJS runtime and context in memory with near-constant time and sub-millisecond startup goals.
3. **Inject privileges**: Zig injects native bindings according to Flory-declared permissions.
   - For example, if a Skill may only access a weather API, Zig exposes a restricted `fetch` and hides filesystem APIs.
4. **Execute and monitor**: Zig runs the JS code and enables interrupt monitoring.
5. **Return**: the result follows the dual-track contract: `raw_data` and `summary`.

#### **2.2.2 Core Security and Isolation Mechanisms**

- **Memory quota allocation**: QuickJS supports custom allocators. Zig can use an allocator such as `FixedBufferAllocator` and impose a hard memory limit, for example 128 MB, on each JS instance. If code allocates too much memory, the allocator stops execution with `OutOfMemory` while keeping the host safe.
- **Time and instruction breaker**: Zig configures QuickJS `JS_SetInterruptHandler`. After a number of bytecode instructions, Zig checks elapsed time and returns `-1` to interrupt scripts exceeding the timeout, for example 10 seconds.
- **Zero syscall leak**: on macOS through `sandbox-exec` when available and on Linux through `seccomp-bpf`, the Zig launcher can lock down OS-level syscall permissions before starting JS execution. Even if QuickJS has a native-level vulnerability, the process remains inside the OS-level cage.

### **2.3 Flory and Aegis Communication Contract (Go <-> Zig)**

Flory and Aegis communicate through standard I/O or JSON-RPC for portability and low overhead.

```json
{
  "jsonrpc": "2.0",
  "method": "executeSkill",
  "params": {
    "task_id": "task_123",
    "source_code": "export default async function main(input) { return { raw_data: input, summary: 'ok' } }",
    "input": {},
    "permissions": {
      "network": ["https://api.weather.example"],
      "fs": []
    },
    "limits": {
      "timeout_ms": 10000,
      "memory_mb": 128
    }
  },
  "id": "req_1"
}
```

With this adapter architecture, Aurora keeps the TypeScript developer ecosystem while building a strong local single-file fortress engine without external container dependencies.
