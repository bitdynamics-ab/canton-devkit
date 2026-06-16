import { describe, it, expect } from "vitest";
import { scopeQ } from "./MetricsScreen";

// scopeQ mirrors the backend metricsq.SummaryQueriesFor(instance) selector
// injection: when the summary reports a shared-stack scope, every metric
// selector gets an instance="<scope>" matcher so a chart shows one instance
// rather than the sum across all instances the shared Prometheus scrapes.

describe("scopeQ", () => {
  it("returns the query unchanged when scope is empty (per-instance path)", () => {
    const q = "sum(rate(daml_participant_api_indexer_updates[1m])) or vector(0)";
    expect(scopeQ(q, "")).toBe(q);
  });

  it("injects instance label into a bare metric selector", () => {
    expect(scopeQ("sum(rate(daml_participant_api_indexer_updates[1m]))", "alpha")).toBe(
      'sum(rate(daml_participant_api_indexer_updates{instance="alpha"}[1m]))',
    );
  });

  it("composes with an existing label without a trailing comma", () => {
    expect(
      scopeQ('sum(jvm_memory_used_bytes{jvm_memory_type="heap"})', "alpha"),
    ).toBe('sum(jvm_memory_used_bytes{instance="alpha",jvm_memory_type="heap"})');
  });

  it("scopes every selector in a multi-metric query", () => {
    const got = scopeQ(
      'sum(rate(daml_grpc_server_handled_total{grpc_code!="OK"}[1m])) or daml_participant_api_indexer_updates',
      "beta",
    );
    expect(got).toBe(
      'sum(rate(daml_grpc_server_handled_total{instance="beta",grpc_code!="OK"}[1m])) or daml_participant_api_indexer_updates{instance="beta"}',
    );
  });

  it("does not touch function names or by() label lists", () => {
    const got = scopeQ(
      "sum by (grpc_method_name) (rate(daml_grpc_server_handled_total[5m]))",
      "beta",
    );
    expect(got).toBe(
      'sum by (grpc_method_name) (rate(daml_grpc_server_handled_total{instance="beta"}[5m]))',
    );
  });

  it("scopes jvm_ and db_client selectors", () => {
    expect(scopeQ("sum(db_client_connections_usage)", "g1")).toBe(
      'sum(db_client_connections_usage{instance="g1"})',
    );
    expect(scopeQ("jvm_threads_live_threads", "g1")).toBe(
      'jvm_threads_live_threads{instance="g1"}',
    );
  });
});
