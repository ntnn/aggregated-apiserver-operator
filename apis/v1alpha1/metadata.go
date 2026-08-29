package v1alpha1

const (
	// ClusterAnnotation names the source cluster on reads and the routing target on writes.
	ClusterAnnotation = "aggregation.ntnn.dev/cluster"

	// ClusterLabel is a virtual label injected on reads for selector-based cluster filtering.
	// It is stripped before writing.
	ClusterLabel = "aggregation.ntnn.dev/cluster"
)
