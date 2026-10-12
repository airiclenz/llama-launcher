---
title: "ADR-0003 trigger to revisit: an upstream fix of llama.cpp #20851 reopens multi-Model hosting on llamacpp"
---

If llama.cpp #20851 is fixed upstream and per-Model hardware overrides start to apply correctly, multi-Model hosting on `llamacpp` becomes a real option to weigh against restart-per-Profile. The trade-off then is "switching cost" vs "memory pressure from N Models warm" — a different conversation than the one this ADR resolved.
