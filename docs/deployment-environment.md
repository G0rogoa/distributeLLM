# 部署环境

目标是一台共享的 Ubuntu 24.04 LTS KVM 虚拟机，配有 5 张 NVIDIA A100 80GB GPU。多个用户在同一个 OS 中直接运行进程，并手动协调 GPU 使用。Slurm、Kubernetes 和容器编排都不是当前 active resource allocator。

DistServe 只能使用组内 policy 明确授权的 GPU。它的目标是在 GPU compute/memory、CPU、host memory、swap 和 storage I/O 维度尽量降低对其他科研工作的影响。主要科研任务优先。

本文档有意省略 host addresses、accounts、credentials、Wi-Fi data、contacts、internal paths 和其他用户 process details。Multi-node deployment 和 data-center-scale scheduling 不在当前范围内。
