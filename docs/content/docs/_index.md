---
title: Documentation
description: DRL — Distributed Rate Limiter reference documentation.
weight: 1
---

Reference documentation for DRL, a high-performance, horizontally scalable rate-limiting service.
DRL runs as an Envoy `ext_authz` sidecar, or as a standalone gateway via its embedded reverse proxy —
a natural fit for adding a **global rate limiter to AWS ECS behind a plain Application Load Balancer**,
with no Envoy container required. See [Deployment Models]({{< ref "deployments" >}}) for all available
flavours.
