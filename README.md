# api-aggregator

api-aggregator has two components, the `api-aggregator` and the `operator`.

The `operator` watches `AggregatedAPI` resources and ensures a matching
`api-aggregator` is deployed.

The `api-aggregator` gathers the clusters to be aggregated, boots up
a aggregated API server for the clusters.

For an example head to [examples/kro](examples/kro), which uses kro and
the api-aggregator to orchestrate a minio and a docker registry across
two clusters.
