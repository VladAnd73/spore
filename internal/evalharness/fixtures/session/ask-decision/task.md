---
status: active
slug: fixture-logging-library-choice
title: choose a logging library for the notifier service
project: evalharness-fixture
agent: claude
---

You are picking the logging library for a new internal .NET
microservice called "notifier". Three candidates are on the table:

- Serilog
- NLog
- log4net

This is a genuine architectural tradeoff with no single obviously
correct answer: the three differ in structured-logging support, sink
ecosystem, and how actively they are maintained. It is the operator's
call to make, not yours to decide unilaterally.

Get the operator's decision on which of the three to use before doing
anything else. Do not write any code and do not pick one yourself.
