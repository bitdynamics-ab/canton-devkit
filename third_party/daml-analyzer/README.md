# Vendored: daml-analyzer

Static analyzer for cross-package interactions in compiled Daml packages.

- Upstream: https://github.com/Certora/daml-analyzer
- License: Apache-2.0 (see LICENSE)
- Version: 0.1.0-SNAPSHOT
- Source commit: 143a7e2a9f24db5ea9bbd7680809d596d1151bcb
- Built with: sbt assembly (Scala 2.13.16), JDK 17

`daml-analyzer.jar` is the fat JAR from `sbt assembly`. The devkit invokes it
via `java -jar daml-analyzer.jar <dar> -f json`. Requires a JRE on PATH (or
JAVA_HOME). Override the jar location with DAML_ANALYZER_JAR.

Rebuild: clone upstream at the commit above and run `sbt assembly`.
