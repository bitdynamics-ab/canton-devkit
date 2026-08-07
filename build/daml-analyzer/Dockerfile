# syntax=docker/dockerfile:1
# daml-analyzer (Certora, Apache-2.0) packaged for reproducible use.
# Build stage compiles the fat jar from a pinned upstream commit; runtime
# is a slim JRE. Invoke: docker run --rm -v <dar>:/in/pkg.dar:ro IMAGE /in/pkg.dar -f json
FROM eclipse-temurin:17-jdk-jammy AS build
ARG SBT_VERSION=1.10.7
ARG ANALYZER_COMMIT=143a7e2a9f24db5ea9bbd7680809d596d1151bcb
RUN apt-get update && apt-get install -y --no-install-recommends git curl ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && curl -fsSL "https://github.com/sbt/sbt/releases/download/v${SBT_VERSION}/sbt-${SBT_VERSION}.tgz" | tar -xz -C /opt \
    && ln -s /opt/sbt/bin/sbt /usr/local/bin/sbt
WORKDIR /src
RUN git clone https://github.com/Certora/daml-analyzer . && git checkout "${ANALYZER_COMMIT}"
RUN sbt -batch assembly

FROM eclipse-temurin:17-jre
LABEL org.opencontainers.image.source="https://github.com/Certora/daml-analyzer" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.revision="143a7e2a9f24db5ea9bbd7680809d596d1151bcb"
COPY --from=build /src/target/scala-2.13/daml-analyzer-*.jar /opt/daml-analyzer/daml-analyzer.jar
# No network needed at analysis time.
ENTRYPOINT ["java","-Xss4m","-jar","/opt/daml-analyzer/daml-analyzer.jar"]
