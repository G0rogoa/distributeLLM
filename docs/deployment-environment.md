# Deployment environment

The target is a single shared Ubuntu 24.04 LTS virtual machine on KVM with
5× NVIDIA A100 80GB GPUs. Multiple users run processes directly in the same OS and
coordinate GPU use manually. Slurm, Kubernetes, and container orchestration are not
the active resource allocator.

DistServe may use only GPUs explicitly authorized under group policy. Its objective is
to minimize impact on other research work across GPU compute and memory, CPU, host
memory, swap, and storage I/O. Primary research jobs take priority.

This document intentionally omits host addresses, accounts, credentials, Wi-Fi data,
contacts, internal paths, and other users' process details. Multi-node deployment and
data-center-scale scheduling are outside the current scope.

