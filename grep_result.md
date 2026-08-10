Makefile:4:	go run ./cmd/aegisd
Makefile:8:	go build -o bin/aegisd ./cmd/aegisd
README.md:1:# AegisRT
README.md:6:AegisRT 是一个面向多 Agent 系统设计的用户态 Runtime。它将 Agent 视为系统级一等执行实体，为 Agent 提供统一的生命周期管理、资源隔离、上下文管理、任务调度、可靠输出和运行状态观测能力。
README.md:8:与传统 Agent 工作流框架不同，AegisRT 关注多 Agent 系统运行过程中的系统级问题，包括资源竞争、上下文冗余、任务依赖、故障传播以及执行结果一致性。
README.md:10:AegisRT 基于 Linux 内核能力构建，结合 cgroup v2、PSI、内容寻址存储和事务化执行机制，为智能体应用提供稳定、可控、可扩展的运行环境。
README.md:18:AegisRT 使用 Agent Control Block（ACB）统一描述 Agent 状态，包括：
README.md:43:AegisRT 基于 Linux cgroup v2 实现 Agent 级资源治理：
README.md:66:AegisRT 提出 CAPS（Context-Affinity and Pressure-aware Scheduling）调度策略。
README.md:83:AegisRT 提供基于内容寻址的 ContextFS。
README.md:132:AegisRT 不允许 Agent 直接修改共享结果。
README.md:169:AegisRT 支持 Agent 间依赖管理。
README.md:197:AegisRT 提供统一事件流和运行状态接口。
README.md:210:aegisctl status
README.md:211:aegisctl agents
README.md:212:aegisctl events
README.md:213:aegisctl metrics
README.md:227:              AegisRT Runtime
README.md:252:aegisrt/
README.md:255:│   ├── aegisd/          # Runtime daemon
README.md:256:│   ├── aegisctl/        # CLI tool
README.md:310:go build -o bin/aegisd ./cmd/aegisd
README.md:311:go build -o bin/aegisctl ./cmd/aegisctl
README.md:327:./bin/aegisctl status
README.md:329:./bin/aegisctl agents
README.md:331:./bin/aegisctl events
README.md:333:./bin/aegisctl metrics
README.md:361:AegisRT 构建了一套面向未来多 Agent 系统的运行时基础设施：
README.md:387:<b>AegisRT — Making Agents First-Class Runtime Citizens.</b>
cmd/aegisctl/main.go:18:	"aegisrt/internal/controlclient"
cmd/aegisctl/main.go:33:			"AEGISRT_SERVER",
cmd/aegisctl/main.go:36:		"AegisRT Runtime API URL",
cmd/aegisctl/main.go:163:				"usage: aegisctl agent <agent-id>",
cmd/aegisctl/main.go:543:  aegisctl [global options] <command> [command options]
cmd/aegisctl/main.go:560:  AEGISRT_SERVER    Default Runtime API URL
cmd/aegisctl/main.go:563:  aegisctl status
cmd/aegisctl/main.go:564:  aegisctl agents -phase FAILED
cmd/aegisctl/main.go:565:  aegisctl agent api-producer-success
cmd/aegisctl/main.go:566:  aegisctl events -since 10
cmd/aegisctl/main.go:567:  aegisctl watch -agent-id api-producer-success
cmd/aegisctl/main.go:568:  aegisctl metrics`,
cmd/aegisctl/main.go:573:	fmt.Fprintln(os.Stderr, "aegisctl:", err)
cmd/aegisd/main.go:14:	"aegisrt/internal/agent"
cmd/aegisd/main.go:15:	"aegisrt/internal/resource"
cmd/aegisd/main.go:16:	agentRuntime "aegisrt/internal/runtime"
cmd/affinitydemo/main.go:15:	"aegisrt/internal/agent"
cmd/affinitydemo/main.go:16:	"aegisrt/internal/contextstore"
cmd/affinitydemo/main.go:17:	"aegisrt/internal/pressure"
cmd/affinitydemo/main.go:18:	"aegisrt/internal/resource"
cmd/affinitydemo/main.go:19:	agentRuntime "aegisrt/internal/runtime"
cmd/affinitydemo/main.go:20:	"aegisrt/internal/scheduler"
cmd/affinitydemo/main.go:109:			Key:       "dataset://aegisrt/shared-corpus",
cmd/affinitydemo/main.go:116:			Key:       "dataset://aegisrt/unrelated-corpus",
cmd/apidemo/main.go:18:	"aegisrt/internal/agent"
cmd/apidemo/main.go:19:	"aegisrt/internal/controlapi"
cmd/apidemo/main.go:20:	"aegisrt/internal/outputtxn"
cmd/apidemo/main.go:21:	"aegisrt/internal/pressure"
cmd/apidemo/main.go:22:	"aegisrt/internal/resource"
cmd/apidemo/main.go:23:	agentRuntime "aegisrt/internal/runtime"
cmd/apidemo/main.go:24:	"aegisrt/internal/scheduler"
cmd/apidemo/main.go:25:	"aegisrt/internal/telemetry"
cmd/capsdemo/main.go:15:	"aegisrt/internal/agent"
cmd/capsdemo/main.go:16:	"aegisrt/internal/pressure"
cmd/capsdemo/main.go:17:	"aegisrt/internal/resource"
cmd/capsdemo/main.go:18:	agentRuntime "aegisrt/internal/runtime"
cmd/capsdemo/main.go:19:	"aegisrt/internal/scheduler"
cmd/cgroupdemo/main.go:9:	"aegisrt/internal/resource"
cmd/contextbridgedemo/main.go:17:	"aegisrt/internal/agent"
cmd/contextbridgedemo/main.go:18:	"aegisrt/internal/contextfs"
cmd/contextbridgedemo/main.go:19:	"aegisrt/internal/contextstore"
cmd/contextbridgedemo/main.go:20:	"aegisrt/internal/pressure"
cmd/contextbridgedemo/main.go:21:	"aegisrt/internal/resource"
cmd/contextbridgedemo/main.go:22:	agentRuntime "aegisrt/internal/runtime"
cmd/contextbridgedemo/main.go:23:	"aegisrt/internal/scheduler"
cmd/contextfsdemo/main.go:11:	"aegisrt/internal/contextfs"
cmd/eventdemo/main.go:15:	"aegisrt/internal/agent"
cmd/eventdemo/main.go:16:	"aegisrt/internal/outputtxn"
cmd/eventdemo/main.go:17:	"aegisrt/internal/pressure"
cmd/eventdemo/main.go:18:	"aegisrt/internal/resource"
cmd/eventdemo/main.go:19:	agentRuntime "aegisrt/internal/runtime"
cmd/eventdemo/main.go:20:	"aegisrt/internal/scheduler"
cmd/eventdemo/main.go:21:	"aegisrt/internal/telemetry"
cmd/integritydagdemo/main.go:16:	"aegisrt/internal/agent"
cmd/integritydagdemo/main.go:17:	"aegisrt/internal/outputtxn"
cmd/integritydagdemo/main.go:18:	"aegisrt/internal/resource"
cmd/integritydagdemo/main.go:19:	agentRuntime "aegisrt/internal/runtime"
cmd/integritydagdemo/main.go:20:	"aegisrt/internal/scheduler"
cmd/psidemo/main.go:8:	"aegisrt/internal/pressure"
cmd/schedulerdemo/main.go:14:	"aegisrt/internal/agent"
cmd/schedulerdemo/main.go:15:	"aegisrt/internal/resource"
cmd/schedulerdemo/main.go:16:	agentRuntime "aegisrt/internal/runtime"
cmd/schedulerdemo/main.go:17:	"aegisrt/internal/scheduler"
cmd/transactiondemo/main.go:16:	"aegisrt/internal/agent"
cmd/transactiondemo/main.go:17:	"aegisrt/internal/contextfs"
cmd/transactiondemo/main.go:18:	"aegisrt/internal/contextstore"
cmd/transactiondemo/main.go:19:	"aegisrt/internal/outputtxn"
cmd/transactiondemo/main.go:20:	"aegisrt/internal/resource"
cmd/transactiondemo/main.go:21:	agentRuntime "aegisrt/internal/runtime"
cmd/workspacedemo/main.go:13:	"aegisrt/internal/contextfs"
cmd/workspaceexecdemo/main.go:17:	"aegisrt/internal/agent"
cmd/workspaceexecdemo/main.go:18:	"aegisrt/internal/contextfs"
cmd/workspaceexecdemo/main.go:19:	"aegisrt/internal/contextstore"
cmd/workspaceexecdemo/main.go:20:	"aegisrt/internal/pressure"
cmd/workspaceexecdemo/main.go:21:	"aegisrt/internal/resource"
cmd/workspaceexecdemo/main.go:22:	agentRuntime "aegisrt/internal/runtime"
cmd/workspaceexecdemo/main.go:23:	"aegisrt/internal/scheduler"
deploy/systemd/aegis-cgroup-demo.service:2:Description=AegisRT cgroup v2 resource demo
deploy/systemd/aegis-cgroup-demo.service:11:WorkingDirectory=/home/linya/workspace/aegisrt
deploy/systemd/aegis-cgroup-demo.service:12:ExecStart=/home/linya/workspace/aegisrt/bin/cgroupdemo
deploy/systemd/aegisrt-affinity-demo.service:2:Description=AegisRT context-affinity scheduling demo
deploy/systemd/aegisrt-affinity-demo.service:11:WorkingDirectory=/home/linya/workspace/aegisrt
deploy/systemd/aegisrt-affinity-demo.service:13:ExecStart=/usr/local/libexec/aegisrt/affinitydemo -worker /home/linya/workspace/aegisrt/worker/python/hello_agent.py
deploy/systemd/aegisrt-api-demo.service:2:Description=AegisRT Runtime HTTP query API
deploy/systemd/aegisrt-api-demo.service:11:WorkingDirectory=/home/linya/workspace/aegisrt
deploy/systemd/aegisrt-api-demo.service:13:ExecStart=/usr/local/libexec/aegisrt/apidemo -listen 127.0.0.1:18080 -integrity-worker /home/linya/workspace/aegisrt/worker/python/integrity_dag_agent.py -transaction-worker /home/linya/workspace/aegisrt/worker/python/transaction_agent.py -root /home/linya/workspace/aegisrt/var/api-demo -reset=true
deploy/systemd/aegisrt-caps-policy-demo.service:2:Description=AegisRT CAPS pressure-aware scheduling policy demo
deploy/systemd/aegisrt-caps-policy-demo.service:11:WorkingDirectory=/home/linya/workspace/aegisrt
deploy/systemd/aegisrt-caps-policy-demo.service:13:ExecStart=/usr/local/libexec/aegisrt/capsdemo -worker /home/linya/workspace/aegisrt/worker/python/profile_agent.py -policy=caps -concurrency=1
deploy/systemd/aegisrt-contextfs-scheduler-demo.service:2:Description=AegisRT ContextFS-backed scheduling demo
deploy/systemd/aegisrt-contextfs-scheduler-demo.service:11:WorkingDirectory=/home/linya/workspace/aegisrt
deploy/systemd/aegisrt-contextfs-scheduler-demo.service:13:ExecStart=/usr/local/libexec/aegisrt/contextbridgedemo -worker /home/linya/workspace/aegisrt/worker/python/hello_agent.py -contextfs-root /home/linya/workspace/aegisrt/var/contextfs-bridge-demo -reset=true
deploy/systemd/aegisrt-demo.service:2:Description=AegisRT integrated Agent resource demo
deploy/systemd/aegisrt-demo.service:11:WorkingDirectory=/home/linya/workspace/aegisrt
deploy/systemd/aegisrt-demo.service:13:ExecStart=/usr/local/libexec/aegisrt/aegisd -worker /home/linya/workspace/aegisrt/worker/python/hello_agent.py -agent-seconds=8 -timeout=15s -cpu-percent=25 -memory-mib=128 -pids-max=16
deploy/systemd/aegisrt-descendant-test.service:2:Description=AegisRT descendant fault-domain cleanup test
deploy/systemd/aegisrt-descendant-test.service:11:WorkingDirectory=/home/linya/workspace/aegisrt
deploy/systemd/aegisrt-descendant-test.service:13:ExecStart=/usr/local/libexec/aegisrt/aegisd -worker /home/linya/workspace/aegisrt/worker/python/descendant_agent.py -agent-seconds=30 -timeout=2s -cpu-percent=100 -memory-mib=128 -pids-max=16
deploy/systemd/aegisrt-event-demo.service:2:Description=AegisRT unified Runtime event demo
deploy/systemd/aegisrt-event-demo.service:11:WorkingDirectory=/home/linya/workspace/aegisrt
deploy/systemd/aegisrt-event-demo.service:13:ExecStart=/usr/local/libexec/aegisrt/eventdemo -integrity-worker /home/linya/workspace/aegisrt/worker/python/integrity_dag_agent.py -transaction-worker /home/linya/workspace/aegisrt/worker/python/transaction_agent.py -root /home/linya/workspace/aegisrt/var/event-demo -reset=true
deploy/systemd/aegisrt-fifo-policy-demo.service:2:Description=AegisRT FIFO scheduling policy demo
deploy/systemd/aegisrt-fifo-policy-demo.service:11:WorkingDirectory=/home/linya/workspace/aegisrt
deploy/systemd/aegisrt-fifo-policy-demo.service:13:ExecStart=/usr/local/libexec/aegisrt/capsdemo -worker /home/linya/workspace/aegisrt/worker/python/profile_agent.py -policy=fifo -concurrency=1
deploy/systemd/aegisrt-integrity-dag-demo.service:2:Description=AegisRT verified-output DAG demo
deploy/systemd/aegisrt-integrity-dag-demo.service:11:WorkingDirectory=/home/linya/workspace/aegisrt
deploy/systemd/aegisrt-integrity-dag-demo.service:13:ExecStart=/usr/local/libexec/aegisrt/integritydagdemo -worker /home/linya/workspace/aegisrt/worker/python/integrity_dag_agent.py -root /home/linya/workspace/aegisrt/var/integrity-dag-demo -reset=true
deploy/systemd/aegisrt-oom-test.service:2:Description=AegisRT Agent OOM fault-domain test
deploy/systemd/aegisrt-oom-test.service:11:WorkingDirectory=/home/linya/workspace/aegisrt
deploy/systemd/aegisrt-oom-test.service:13:ExecStart=/usr/local/libexec/aegisrt/aegisd -worker /home/linya/workspace/aegisrt/worker/python/oom_agent.py -agent-seconds=30 -timeout=15s -cpu-percent=100 -memory-mib=64 -pids-max=16
deploy/systemd/aegisrt-scheduler-demo.service:2:Description=AegisRT concurrent multi-Agent Scheduler demo
deploy/systemd/aegisrt-scheduler-demo.service:11:WorkingDirectory=/home/linya/workspace/aegisrt
deploy/systemd/aegisrt-scheduler-demo.service:13:ExecStart=/usr/local/libexec/aegisrt/schedulerdemo -worker /home/linya/workspace/aegisrt/worker/python/hello_agent.py -concurrency=2
deploy/systemd/aegisrt-transaction-demo.service:2:Description=AegisRT transactional Agent output demo
deploy/systemd/aegisrt-transaction-demo.service:11:WorkingDirectory=/home/linya/workspace/aegisrt
deploy/systemd/aegisrt-transaction-demo.service:13:ExecStart=/usr/local/libexec/aegisrt/transactiondemo -worker /home/linya/workspace/aegisrt/worker/python/transaction_agent.py -root /home/linya/workspace/aegisrt/var/output-transaction-demo -reset=true
deploy/systemd/aegisrt-workspace-execution-demo.service:2:Description=AegisRT ContextFS workspace execution demo
deploy/systemd/aegisrt-workspace-execution-demo.service:11:WorkingDirectory=/home/linya/workspace/aegisrt
deploy/systemd/aegisrt-workspace-execution-demo.service:13:ExecStart=/usr/local/libexec/aegisrt/workspaceexecdemo -worker /home/linya/workspace/aegisrt/worker/python/workspace_agent.py -root /home/linya/workspace/aegisrt/var/contextfs-execution-demo -reset=true
go.mod:1:module aegisrt
internal/agent/model.go:6:	"aegisrt/internal/contextstore"
internal/agent/model.go:7:	"aegisrt/internal/resource"
internal/contextstore/resolver.go:7:	"aegisrt/internal/contextfs"
internal/contextstore/resolver_test.go:7:	"aegisrt/internal/contextfs"
internal/controlapi/api.go:12:	"aegisrt/internal/scheduler"
internal/controlapi/api.go:13:	"aegisrt/internal/telemetry"
internal/controlapi/api.go:39:// API implements the AegisRT HTTP query plane.
internal/controlapi/api_test.go:10:	"aegisrt/internal/scheduler"
internal/controlapi/api_test.go:11:	"aegisrt/internal/telemetry"
internal/controlapi/observability.go:8:	"aegisrt/internal/scheduler"
internal/controlapi/observability.go:99:		"# HELP aegisrt_up Whether the AegisRT HTTP process is alive.",
internal/controlapi/observability.go:103:		"# TYPE aegisrt_up gauge",
internal/controlapi/observability.go:105:	fmt.Fprintln(writer, "aegisrt_up 1")
internal/controlapi/observability.go:109:		"# HELP aegisrt_ready Whether the Runtime is ready to accept work.",
internal/controlapi/observability.go:113:		"# TYPE aegisrt_ready gauge",
internal/controlapi/observability.go:117:		"aegisrt_ready %d\n",
internal/controlapi/observability.go:123:		"# HELP aegisrt_runtime_uptime_seconds Runtime API uptime.",
internal/controlapi/observability.go:127:		"# TYPE aegisrt_runtime_uptime_seconds gauge",
internal/controlapi/observability.go:131:		"aegisrt_runtime_uptime_seconds %.6f\n",
internal/controlapi/observability.go:137:		"# HELP aegisrt_scheduler_started Whether the Scheduler has started.",
internal/controlapi/observability.go:141:		"# TYPE aegisrt_scheduler_started gauge",
internal/controlapi/observability.go:145:		"aegisrt_scheduler_started %d\n",
internal/controlapi/observability.go:151:		"# HELP aegisrt_scheduler_stopped Whether the Scheduler has stopped.",
internal/controlapi/observability.go:155:		"# TYPE aegisrt_scheduler_stopped gauge",
internal/controlapi/observability.go:159:		"aegisrt_scheduler_stopped %d\n",
internal/controlapi/observability.go:165:		"# HELP aegisrt_scheduler_workers Configured Scheduler workers.",
internal/controlapi/observability.go:169:		"# TYPE aegisrt_scheduler_workers gauge",
internal/controlapi/observability.go:173:		"aegisrt_scheduler_workers %d\n",
internal/controlapi/observability.go:179:		"# HELP aegisrt_scheduler_queue_depth Current queued Agent count.",
internal/controlapi/observability.go:183:		"# TYPE aegisrt_scheduler_queue_depth gauge",
internal/controlapi/observability.go:187:		"aegisrt_scheduler_queue_depth %d\n",
internal/controlapi/observability.go:193:		"# HELP aegisrt_scheduler_queue_capacity Maximum waiting queue size.",
internal/controlapi/observability.go:197:		"# TYPE aegisrt_scheduler_queue_capacity gauge",
internal/controlapi/observability.go:201:		"aegisrt_scheduler_queue_capacity %d\n",
internal/controlapi/observability.go:207:		"# HELP aegisrt_scheduler_agents Number of Agents by phase.",
internal/controlapi/observability.go:211:		"# TYPE aegisrt_scheduler_agents gauge",
internal/controlapi/observability.go:225:			"aegisrt_scheduler_agents{phase=%q} %d\n",
internal/controlapi/observability.go:239:		"# HELP aegisrt_event_bus_published_total Events accepted by the bus.",
internal/controlapi/observability.go:243:		"# TYPE aegisrt_event_bus_published_total counter",
internal/controlapi/observability.go:247:		"aegisrt_event_bus_published_total %d\n",
internal/controlapi/observability.go:253:		"# HELP aegisrt_event_bus_delivered_total Events delivered to sinks.",
internal/controlapi/observability.go:257:		"# TYPE aegisrt_event_bus_delivered_total counter",
internal/controlapi/observability.go:261:		"aegisrt_event_bus_delivered_total %d\n",
internal/controlapi/observability.go:267:		"# HELP aegisrt_event_bus_sink_errors_total Event sink failures.",
internal/controlapi/observability.go:271:		"# TYPE aegisrt_event_bus_sink_errors_total counter",
internal/controlapi/observability.go:275:		"aegisrt_event_bus_sink_errors_total %d\n",
internal/controlapi/observability.go:281:		"# HELP aegisrt_event_bus_queue_depth Current event queue depth.",
internal/controlapi/observability.go:285:		"# TYPE aegisrt_event_bus_queue_depth gauge",
internal/controlapi/observability.go:289:		"aegisrt_event_bus_queue_depth %d\n",
internal/controlapi/observability.go:295:		"# HELP aegisrt_event_bus_queue_capacity Event queue capacity.",
internal/controlapi/observability.go:299:		"# TYPE aegisrt_event_bus_queue_capacity gauge",
internal/controlapi/observability.go:303:		"aegisrt_event_bus_queue_capacity %d\n",
internal/controlapi/observability.go:309:		"# HELP aegisrt_event_sequence Latest allocated event sequence.",
internal/controlapi/observability.go:313:		"# TYPE aegisrt_event_sequence gauge",
internal/controlapi/observability.go:317:		"aegisrt_event_sequence %d\n",
internal/controlapi/observability_test.go:9:	"aegisrt/internal/scheduler"
internal/controlapi/observability_test.go:10:	"aegisrt/internal/telemetry"
internal/controlapi/observability_test.go:135:		"aegisrt_up 1",
internal/controlapi/observability_test.go:136:		"aegisrt_ready 1",
internal/controlapi/observability_test.go:137:		`aegisrt_scheduler_agents{phase="SUCCEEDED"} 3`,
internal/controlapi/observability_test.go:138:		`aegisrt_scheduler_agents{phase="FAILED"} 1`,
internal/controlapi/observability_test.go:139:		"aegisrt_event_bus_published_total 10",
internal/controlapi/observability_test.go:140:		"aegisrt_event_sequence 10",
internal/controlclient/client.go:39:// Client is a small AegisRT Runtime API client.
internal/outputtxn/verify.go:16:	"aegisrt/internal/agent"
internal/outputtxn/verify_test.go:9:	"aegisrt/internal/agent"
internal/runtime/output_executor.go:8:	"aegisrt/internal/agent"
internal/runtime/output_executor.go:9:	"aegisrt/internal/outputtxn"
internal/runtime/output_executor_test.go:10:	"aegisrt/internal/agent"
internal/runtime/output_executor_test.go:11:	"aegisrt/internal/outputtxn"
internal/runtime/process_config.go:9:	"aegisrt/internal/agent"
internal/runtime/process_config_test.go:8:	"aegisrt/internal/agent"
internal/runtime/runner.go:12:	"aegisrt/internal/agent"
internal/runtime/runner.go:13:	"aegisrt/internal/resource"
internal/runtime/workspace_executor.go:11:	"aegisrt/internal/agent"
internal/runtime/workspace_executor.go:12:	"aegisrt/internal/contextfs"
internal/runtime/workspace_executor_test.go:10:	"aegisrt/internal/agent"
internal/runtime/workspace_executor_test.go:11:	"aegisrt/internal/contextfs"
internal/runtime/workspace_executor_test.go:12:	"aegisrt/internal/contextstore"
internal/scheduler/context_policy_test.go:7:	"aegisrt/internal/pressure"
internal/scheduler/context_scheduler_test.go:9:	"aegisrt/internal/agent"
internal/scheduler/context_scheduler_test.go:10:	"aegisrt/internal/contextstore"
internal/scheduler/contextfs_scheduler_test.go:8:	"aegisrt/internal/agent"
internal/scheduler/contextfs_scheduler_test.go:9:	"aegisrt/internal/contextfs"
internal/scheduler/contextfs_scheduler_test.go:10:	"aegisrt/internal/contextstore"
internal/scheduler/dag_test.go:11:	"aegisrt/internal/agent"
internal/scheduler/dependency.go:10:	"aegisrt/internal/agent"
internal/scheduler/events.go:7:	"aegisrt/internal/telemetry"
internal/scheduler/events_test.go:9:	"aegisrt/internal/agent"
internal/scheduler/events_test.go:10:	"aegisrt/internal/telemetry"
internal/scheduler/output_verification_test.go:10:	"aegisrt/internal/agent"
internal/scheduler/output_verifier.go:8:	"aegisrt/internal/agent"
internal/scheduler/policy.go:8:	"aegisrt/internal/pressure"
internal/scheduler/policy_test.go:7:	"aegisrt/internal/pressure"
internal/scheduler/query_test.go:8:	"aegisrt/internal/agent"
internal/scheduler/scheduler.go:11:	"aegisrt/internal/agent"
internal/scheduler/scheduler.go:12:	"aegisrt/internal/contextstore"
internal/scheduler/scheduler.go:13:	"aegisrt/internal/pressure"
internal/scheduler/scheduler.go:14:	"aegisrt/internal/resource"
internal/scheduler/scheduler_test.go:9:	"aegisrt/internal/agent"
internal/telemetry/event.go:25:// Event is the common observable record emitted by AegisRT.
internal/telemetry/event.go:53:		source = "aegisrt"
worker/python/descendant_agent.py:48:            "aegisrt-descendant-marker",
worker/python/hello_agent.py:20:    parser = argparse.ArgumentParser(description="AegisRT hello Agent")
worker/python/hello_agent.py:42:        output="hello from the first AegisRT Agent",
worker/python/profile_agent.py:75:        prefix="aegisrt-io-",
