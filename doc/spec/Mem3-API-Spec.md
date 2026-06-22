# Mem3 API Schema

本规范定义 DAG 初始化写入、Task 完成写入、Task 启动前 Search/List 的标准 JSON 契约。

## 1. Ingest Request

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://aurora/spec/mem3-ingest-request.schema.json",
  "title": "Mem3IngestRequest",
  "type": "object",
  "additionalProperties": false,
  "required": ["version", "idempotency_key", "kind", "scope", "payload"],
  "properties": {
    "version": { "const": "1.0" },
    "idempotency_key": { "type": "string", "minLength": 1 },
    "kind": { "enum": ["DAG_CONTEXT", "TASK_OUTPUT"] },
    "scope": {
      "type": "object",
      "additionalProperties": false,
      "required": ["tenant_id", "agent_id", "session_id", "dag_id"],
      "properties": {
        "tenant_id": { "type": "string", "minLength": 1 },
        "agent_id": { "type": "string", "minLength": 1 },
        "user_id": { "type": "string", "minLength": 1 },
        "session_id": { "type": "string", "minLength": 1 },
        "dag_id": { "type": "string", "minLength": 1 }
      }
    },
    "payload": {
      "oneOf": [
        { "$ref": "#/$defs/dagContext" },
        { "$ref": "#/$defs/taskOutput" }
      ]
    }
  },
  "allOf": [
    {
      "if": { "properties": { "kind": { "const": "DAG_CONTEXT" } } },
      "then": { "properties": { "payload": { "$ref": "#/$defs/dagContext" } } }
    },
    {
      "if": { "properties": { "kind": { "const": "TASK_OUTPUT" } } },
      "then": { "properties": { "payload": { "$ref": "#/$defs/taskOutput" } } }
    }
  ],
  "$defs": {
    "relation": {
      "type": "object",
      "additionalProperties": false,
      "required": ["subject", "predicate", "object", "memory_scope"],
      "properties": {
        "subject": { "type": "string", "minLength": 1 },
        "predicate": { "type": "string", "minLength": 1 },
        "object": { "type": "string", "minLength": 1 },
        "memory_scope": { "enum": ["DAG", "SESSION", "AGENT", "TENANT"] },
        "confidence": { "type": "number", "minimum": 0, "maximum": 1 }
      }
    },
    "memoryItem": {
      "type": "object",
      "additionalProperties": false,
      "required": ["value", "memory_scope"],
      "properties": {
        "value": { "type": "string", "minLength": 1 },
        "memory_scope": { "enum": ["DAG", "SESSION", "AGENT", "TENANT"] },
        "confidence": { "type": "number", "minimum": 0, "maximum": 1 }
      }
    },
    "dagContext": {
      "type": "object",
      "additionalProperties": false,
      "required": ["raw_query", "intent_slot", "observed_at"],
      "properties": {
        "raw_query": { "type": "string", "minLength": 1 },
        "intent_slot": {
          "type": "object",
          "additionalProperties": false,
          "required": ["macro_intent", "entities", "action_verbs"],
          "properties": {
            "macro_intent": { "type": "string", "minLength": 1 },
            "entities": {
              "type": "array",
              "items": { "type": "string", "minLength": 1 }
            },
            "temporal_context": { "type": "string" },
            "action_verbs": {
              "type": "array",
              "items": { "type": "string", "minLength": 1 }
            },
            "extraction_hint": { "type": "string" }
          }
        },
        "observed_at": { "type": "string", "format": "date-time" }
      }
    },
    "taskOutput": {
      "type": "object",
      "additionalProperties": false,
      "required": [
        "task_id",
        "sequence",
        "node_type",
        "output",
        "completed_at"
      ],
      "properties": {
        "task_id": { "type": "string", "minLength": 1 },
        "parent_task_ids": {
          "type": "array",
          "items": { "type": "string", "minLength": 1 },
          "uniqueItems": true
        },
        "sequence": { "type": "integer", "minimum": 0 },
        "node_type": { "enum": ["skill", "planner"] },
        "skill_name": { "type": "string" },
        "output": {},
        "skill_summary": { "type": "string" },
        "completed_at": { "type": "string", "format": "date-time" }
      }
    }
  }
}
```

## 2. Ingest Response

HTTP 状态码使用 `202 Accepted`。

```json
{
  "version": "1.0",
  "ingest_id": "ing_123",
  "accepted": true,
  "async_status": "QUEUED",
  "summary_version": null
}
```

`async_status` 枚举为 `QUEUED | PROCESSING | COMPLETED | FAILED`。Task reduce 完成后产生新的 `summary_version`。

## 3. DAG Context Async Extraction Result

以下是 Mem3 消费 `DAG_CONTEXT` 后由轻量 LLM 生成的内部结构，不是 Intent Router 的同步输出：

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://aurora/spec/mem3-dag-extraction-result.schema.json",
  "title": "Mem3DagExtractionResult",
  "type": "object",
  "additionalProperties": false,
  "required": ["ingest_id", "goals", "profile", "facts", "relations"],
  "properties": {
    "ingest_id": { "type": "string", "minLength": 1 },
    "goals": {
      "type": "array",
      "items": { "$ref": "mem3-ingest-request.schema.json#/$defs/memoryItem" }
    },
    "profile": {
      "type": "array",
      "items": { "$ref": "mem3-ingest-request.schema.json#/$defs/memoryItem" }
    },
    "facts": {
      "type": "array",
      "items": { "$ref": "mem3-ingest-request.schema.json#/$defs/memoryItem" }
    },
    "relations": {
      "type": "array",
      "items": { "$ref": "mem3-ingest-request.schema.json#/$defs/relation" }
    },
    "observed_at": { "type": "string", "format": "date-time" }
  }
}
```

只有通过置信度、来源和记忆提升策略校验的条目才能写入 `AGENT` 或 `TENANT` 作用域。

## 4. List Request and Response

```json
{
  "version": "1.0",
  "scope": {
    "tenant_id": "tenant_1",
    "agent_id": "agent_1",
    "session_id": "session_1",
    "dag_id": "dag_1"
  },
  "limit": 5,
  "before_sequence": 12
}
```

```json
{
  "version": "1.0",
  "recent_outputs": [
    {
      "task_id": "task_11",
      "sequence": 11,
      "node_type": "skill",
      "output": {}
    }
  ],
  "latest_summary": {
    "summary": "截至 task_11 的滚动摘要",
    "summary_version": 11,
    "through_sequence": 11
  }
}
```

`recent_outputs` 按 `sequence` 升序返回，便于直接注入 Task 上下文；查询实现可以先倒序取 N 条再反转。

## 5. Search Request

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://aurora/spec/mem3-search-request.schema.json",
  "title": "Mem3SearchRequest",
  "type": "object",
  "additionalProperties": false,
  "required": [
    "version",
    "scope",
    "current_task",
    "recent_limit",
    "mem_hint"
  ],
  "properties": {
    "version": { "const": "1.0" },
    "scope": {
      "type": "object",
      "additionalProperties": false,
      "required": ["tenant_id", "agent_id", "session_id", "dag_id"],
      "properties": {
        "tenant_id": { "type": "string", "minLength": 1 },
        "agent_id": { "type": "string", "minLength": 1 },
        "user_id": { "type": "string", "minLength": 1 },
        "session_id": { "type": "string", "minLength": 1 },
        "dag_id": { "type": "string", "minLength": 1 }
      }
    },
    "current_task": {
      "type": "object",
      "additionalProperties": false,
      "required": [
        "task_id",
        "sequence",
        "node_type",
        "parent_task_ids",
        "mem_hint_source_task_ids"
      ],
      "properties": {
        "task_id": { "type": "string", "minLength": 1 },
        "sequence": { "type": "integer", "minimum": 0 },
        "node_type": { "enum": ["skill", "planner"] },
        "parent_task_ids": {
          "type": "array",
          "items": { "type": "string", "minLength": 1 },
          "uniqueItems": true
        },
        "mem_hint_source_task_ids": {
          "type": "array",
          "items": { "type": "string", "minLength": 1 },
          "uniqueItems": true,
          "description": "生成最终 mem_hint 时使用的父 Task；根 Task 为空数组"
        }
      }
    },
    "recent_limit": { "type": "integer", "minimum": 0, "maximum": 50 },
    "mem_hint": {
      "$ref": "https://aurora/spec/mem-hint.schema.json#/properties/mem_hint"
    }
  }
}
```

## 6. Search Response

```json
{
  "version": "1.0",
  "working_memory": {
    "recent_outputs": [],
    "latest_summary": {
      "summary": "",
      "summary_version": 0,
      "through_sequence": -1
    }
  },
  "retrieval": {
    "strategy": "NONE",
    "items": []
  },
  "consistency": {
    "latest_ingested_sequence": 10,
    "summary_through_sequence": 9,
    "summary_pending": true
  }
}
```

`working_memory` 无条件返回。`retrieval.items` 才受 `mem_hint.strategy` 控制。
