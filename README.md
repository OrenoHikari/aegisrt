# AegisRT

> 面向多智能体系统的 AI-Native Agent Runtime
> **统一调度 · 资源隔离 · 上下文复用 · 事务输出 · 系统级可观测性**

AegisRT 是一个面向多 Agent 系统设计的用户态 Runtime。它将 Agent 视为系统级一等执行实体，为 Agent 提供统一的生命周期管理、资源隔离、上下文管理、任务调度、可靠输出和运行状态观测能力。

与传统 Agent 工作流框架不同，AegisRT 关注多 Agent 系统运行过程中的系统级问题，包括资源竞争、上下文冗余、任务依赖、故障传播以及执行结果一致性。

AegisRT 基于 Linux 内核能力构建，结合 cgroup v2、PSI、内容寻址存储和事务化执行机制，为智能体应用提供稳定、可控、可扩展的运行环境。

---

## 核心能力

### Agent 一等公民模型

AegisRT 使用 Agent Control Block（ACB）统一描述 Agent 状态，包括：

* Agent 身份与生命周期；
* 资源预算；
* 执行环境；
* 上下文引用；
* DAG 依赖关系；
* 输出状态；
* 运行时统计信息。

Agent 生命周期：

```
CREATED → READY → RUNNING → SUCCEEDED
                         └→ FAILED
```

每个 Agent 都拥有独立运行状态，可被调度、监控和管理。

---

## 核心特性

### 1. 资源隔离与故障域管理

AegisRT 基于 Linux cgroup v2 实现 Agent 级资源治理：

* CPU 配额限制；
* 内存限制；
* PID 数量限制；
* OOM 状态监控；
* 进程树统一清理。

每个 Agent 运行于独立资源域中，避免单个任务影响整个 Runtime。

```
Agent
 ├── main process
 ├── child process
 └── subprocess

统一属于 Agent cgroup
```

---

### 2. 压力感知调度

AegisRT 提出 CAPS（Context-Affinity and Pressure-aware Scheduling）调度策略。

调度过程综合考虑：

* Linux PSI CPU 压力；
* Memory 压力；
* I/O 压力；
* Agent 等待时间；
* 上下文复用情况；
* 资源需求。

相比传统 FIFO 调度，CAPS 能够根据系统状态动态调整任务执行顺序，提高多 Agent 场景下的资源利用效率。

---

### 3. ContextFS 上下文管理

AegisRT 提供基于内容寻址的 ContextFS。

核心机制：

```
Context Object
       │
       ▼
   SHA-256 Digest
       │
       ▼
 Immutable Blob
```

支持：

* 内容寻址；
* 自动去重；
* 上下文复用；
* 引用管理；
* 安全回收。

不同逻辑名称可以共享同一个物理上下文对象，减少重复存储和加载开销。

---

### 4. Agent 隔离工作区

Runtime 为每个 Agent 创建独立工作空间：

```
workspace/

├── inputs/
├── private/
└── manifest.json
```

设计目标：

* 上下文对象保持不可变；
* Agent 修改不会污染共享数据；
* 不同 Agent 工作环境相互隔离；
* 支持安全执行和结果验证。

---

### 5. 事务化输出机制

AegisRT 不允许 Agent 直接修改共享结果。

执行流程：

```
Agent Execution

      ↓

Private Staging

      ↓

Validation

      ↓

Atomic Commit

      ↓

Verified Output
```

提交过程中会检查：

* 文件完整性；
* 输出结构；
* SHA-256 校验；
* Manifest 信息。

只有验证通过的结果才会进入共享输出空间。

---

### 6. DAG 依赖与故障传播

AegisRT 支持 Agent 间依赖管理。

下游任务只有在上游：

```
Execution Success
        +
Output Commit
        +
Integrity Verification
```

全部满足后才会执行。

失败任务会自动阻断相关依赖：

```
FAILED
   ↓
BLOCKED
```

避免错误结果继续传播。

---

### 7. 系统级可观测性

AegisRT 提供统一事件流和运行状态接口。

支持：

* Agent 状态查询；
* Runtime 状态监控；
* 事件追踪；
* Prometheus 指标；
* CLI 查询工具。

示例：

```bash
aegisctl status
aegisctl agents
aegisctl events
aegisctl metrics
```

---

# 系统架构

```
        Agent Framework / Application

                    │

                    ▼

              AegisRT Runtime

 ┌─────────────────────────────────┐
 │ Scheduler                        │
 │                                  │
 │  CAPS Policy                     │
 │  DAG Dependency Manager          │
 │  Agent Lifecycle Manager         │
 │                                  │
 ├─────────────────────────────────┤
 │ ContextFS                        │
 │ Output Transaction System        │
 │ Event Stream                     │
 │                                  │
 ├─────────────────────────────────┤
 │ Linux Kernel                     │
 │ cgroup v2 / PSI / Process        │
 └─────────────────────────────────┘
```

---

# 项目结构

```
aegisrt/

├── cmd/
│   ├── aegisd/          # Runtime daemon
│   ├── aegisctl/        # CLI tool
│   ├── apidemo/         # HTTP API demo
│   └── demos/           # Feature demonstrations
│
├── internal/
│   ├── agent/           # Agent model
│   ├── scheduler/       # Scheduler and policies
│   ├── runtime/         # Execution engine
│   ├── resource/        # cgroup management
│   ├── pressure/        # PSI monitoring
│   ├── contextfs/       # Context storage
│   ├── outputtxn/       # Transaction output
│   ├── telemetry/       # Event system
│   └── controlapi/      # Runtime API
│
├── worker/
│   └── python/          # Example agents
│
├── deploy/
│   └── systemd/         # Deployment examples
│
└── README.md
```

---

# 运行环境

| Component | Version                 |
| --------- | ----------------------- |
| OS        | openEuler 24.03 LTS SP4 |
| Kernel    | 6.6+                    |
| Go        | 1.23+                   |
| Python    | 3.9+                    |
| cgroup    | v2                      |
| PSI       | Enabled                 |

---

# 快速开始

## 构建

```bash
go test ./...

go build ./cmd/...
```

构建 Runtime：

```bash
mkdir -p bin

go build -o bin/aegisd ./cmd/aegisd
go build -o bin/aegisctl ./cmd/aegisctl
```

---

## 启动 Demo Runtime

```bash
./bin/apidemo \
  -disable-cgroup \
  -listen 127.0.0.1:18080
```

查询 Runtime：

```bash
./bin/aegisctl status

./bin/aegisctl agents

./bin/aegisctl events

./bin/aegisctl metrics
```

---

# Runtime API

默认地址：

```
http://127.0.0.1:18080
```

主要接口：

| API                  | Description        |
| -------------------- | ------------------ |
| `/healthz`           | Runtime health     |
| `/readyz`            | Runtime readiness  |
| `/metrics`           | Prometheus metrics |
| `/v1/runtime/status` | Runtime status     |
| `/v1/agents`         | Agent list         |
| `/v1/events`         | Event stream       |

---

# 技术亮点总结

AegisRT 构建了一套面向未来多 Agent 系统的运行时基础设施：

* 将 Agent 从应用层对象提升为系统级执行实体；
* 利用 Linux 原生能力实现资源隔离；
* 通过压力感知调度提升多任务运行效率；
* 通过 ContextFS 实现上下文复用；
* 通过事务输出保证任务结果可靠性；
* 通过 DAG 管理复杂 Agent 依赖；
* 通过统一事件流实现运行时可观测。

---

# Future Work

未来将进一步探索：

* 多节点 Agent Runtime；
* GPU 与异构资源调度；
* LLM 推理服务集成；
* KV Cache / Prefix Cache 感知优化；
* 可视化控制平台；
* 大规模 Agent 工作负载评测。

---

<p align="center">
<b>AegisRT — Making Agents First-Class Runtime Citizens.</b>
</p>
