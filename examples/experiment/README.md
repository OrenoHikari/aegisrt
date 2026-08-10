# Autonomous Experiment Demo dataset

这个目录是一份可编辑的、安全声明式实验工作区：

- `capsule-experiment.json` 选择数据集和三个已注册方法；
- `classification.csv` 是微型确定性三分类数据集。

Agent 会先通过 `experiment.manifest.inspect` Worker 实际查看目录并验证配置，再根据 Observation 生成实验 DAG。Manifest 不能声明脚本、命令、可执行文件或任意环境变量；所有路径都限制在配置的 workspace root 内。

数据集只用于驱动真实的 Dataset Preparation Worker 和验证依赖链；三种方法属于 CPU-only deterministic simulation，准确率与 working-set 是演示夹具，不声称是机器学习 benchmark。Worker 进程、CPU 工作、runtime 测量、调度、失败、Observation、Re-plan、输出事务均为现场执行。

运行：

```bash
make build
./bin/capsulectl experiment demo \
  --workspace-root . \
  --experiment-dir examples/experiment \
  --task "读取实验目录中的设置，比较方法并在资源失败后自主恢复"
```

预期过程：Plan v1 读取并验证 Manifest；Observation 驱动 Plan v2。Random Forest `n_estimators=1000` 超过 64 MiB 预算并失败，第二个结构化 Observation 驱动 Plan v3，复用目录检查、数据集和其他成功任务，以 `n_estimators=100` 重试并生成 `experiment_report.md`。
