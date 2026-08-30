# kro across clusters

This example shows using the api-aggregator and kro to orchestrate
resources across multiple clusters.

The api-aggregator and kro are installed on the `host` kind cluster,
which also carries the CRDs and resources.

Two additional kind clusters `storage` and `registry` are created.
The `storage` cluster will host a MinIO instance, which in turn is used
by an oci registry on the `registry` cluster.

<!--
```bash ci
set -euo pipefail
trap 'kind delete cluster --name host; kind delete cluster --name storage; kind delete cluster --name registry' EXIT
```
-->

## Images

Build the operator and api-aggregator images.

```bash ci
make images IMAGE_TAG=dev
```

## Clusters

Create the kind clusters and preload the built images into the host
cluster.

```bash ci
kind create cluster --name host
kind create cluster --name storage
kind create cluster --name registry
kind load docker-image --name host \
    ghcr.io/ntnn/aggregated-apiserver-operator/api-aggregator:dev \
    ghcr.io/ntnn/aggregated-apiserver-operator/operator:dev
```

## Operator

Install the operator, which deploys an api-aggregator for each
`AggregateAPI` resource. The `examples/kro/operator` is only overlaying
the `config/operator` kustomization with the `:dev` image prefix.

```bash ci
kubectl --context kind-host apply -k examples/kro/operator
kubectl --context kind-host -n aggregated-apiserver-operator rollout status \
    deployment/aggregated-apiserver-operator --timeout 120s
```

## Kubeconfig Secrets

The api-aggregator needs access to the clusters of the APIs it
aggregates. Currently this is implemented by providing secrets with
kubeconfigs inside of them.

For each cluster kind cluster create one secret:

```bash ci
kubectl --context kind-host create secret generic host \
    --from-file=kubeconfig=<(kind get kubeconfig --internal --name host)
kubectl --context kind-host create secret generic storage \
    --from-file=kubeconfig=<(kind get kubeconfig --internal --name storage)
kubectl --context kind-host create secret generic registry \
    --from-file=kubeconfig=<(kind get kubeconfig --internal --name registry)
```

The `--internal` instructs kind to build a kubeconfig that is valid
within the kind network - which is where api-aggregator runs.

## AggregatedAPI

Now create the `AggregatedAPI`:

```bash ci
kubectl --context kind-host apply -f examples/kro/aggregatedapi.yaml
```

Each kind cluster has one entry in `clusters` and a list of APIs the
api-aggregator should expose from this server.

For `storage` and `registry` its the same APIs, in these cases it is
important to use the label or annotation `aggregation.ntnn.dev/cluster`
to target the right cluster when reading or writing.

Lists of course work across all clusters, so e.g. list all pods on the
api-aggregator will returns all pods from `storage` and `registry`.

```bash ci
kubectl --context kind-host -n aggregated-apiserver-operator rollout status \
    deployment/aggregator-default-kro-example --timeout 120s
```

The api-aggregator creates a kubeconfig secret to access the aggregated API:

```bash ci
kubectl --context kind-host get secrets kro-example-kubeconfig -o yaml
```

<!--
```bash ci
kubectl --context kind-host wait --for=jsonpath='{.status.kubeconfigSecret}'=kro-example-kubeconfig \
    aggregatedapi/kro-example --timeout 120s
```
-->

Port forward the service of the `AggregatedAPI`:

```bash ci
kubectl --context kind-host -n aggregated-apiserver-operator port-forward \
    service/aggregator-default-kro-example 8443:443 &>/dev/null &
```

And access it:

```bash ci
kubectl --kubeconfig examples/kro/aggregated.kubeconfig api-resources
```

## kro

Now install kro, which will actually orchestrate the resources across
the clusters through the api-aggregator.

```bash ci
kubectl --context kind-host apply -k examples/kro/kro
kubectl --context kind-host rollout status deployment/kro --timeout 120s
```


<!--
```bash ci
until kubectl --kubeconfig examples/kro/aggregated.kubeconfig get resourcegraphdefinitions.kro.run 2>/dev/null; do
    sleep 2
done
```
-->

## ResourceGraphDefinition

And install the RGD into the aggregated API:

```bash ci
kubectl --kubeconfig examples/kro/aggregated.kubeconfig apply -f examples/kro/rgd.yaml
```

kro will throw a few errors until the discovery of the api-aggregator
has cauught up and is offering the APIs kro expects.

The RGD deploys Minio on the `storage` kind cluster with a node port
service and then deploys a registry on the `registry` kind cluster,
templating in the information from the Minio deployment.

```bash ci
kubectl --kubeconfig examples/kro/aggregated.kubeconfig wait --for=jsonpath='{.status.state}'=Active \
    resourcegraphdefinition/registry-minio --timeout 300s
```

## Instance

And create an instance of the RGD:

```bash ci
kubectl --kubeconfig examples/kro/aggregated.kubeconfig apply -f examples/kro/instance.yaml
```

After creating the instance the deployments and services on the storage
and registry clusters will pop up:

```bash
kubectl --context kind-storage get deployment,service
kubectl --context kind-registry get deployment,service
```

And the same are visible through the aggregated API:

```bash
kubectl --kubeconfig examples/kro/aggregated.kubeconfig get deployment,service
```

<!--
```bash ci
until kubectl --context kind-registry get deployment demo-registry 2>/dev/null; do
    sleep 2
done
kubectl --context kind-storage wait --for=condition=Available \
    deployment/demo-minio --timeout 300s
kubectl --context kind-registry wait --for=condition=Available \
    deployment/demo-registry --timeout 300s
```
-->

## Use it

Now the services are deploys and ready - create port forwards to use them:

```bash ci
kubectl --context kind-registry port-forward service/demo-registry 5001:5000 &>/dev/null &
kubectl --context kind-storage port-forward service/demo-minio 9000:9000 &>/dev/null &
```

Confirm that Minio is empty:

```bash ci
curl -s --user minioadmin:minioadmin --aws-sigv4 aws:amz:us-east-1:s3 \
    'http://localhost:9000/registry?list-type=2&prefix=docker/registry/'
```

Now push the operator image built earlier into the registry:

```bash ci
docker tag ghcr.io/ntnn/aggregated-apiserver-operator/operator:dev \
    localhost:5001/ghcr.io/ntnn/aggregated-apiserver-operator/operator:dev
docker push localhost:5001/ghcr.io/ntnn/aggregated-apiserver-operator/operator:dev
```

And confirm that the blobs landed in Minio:

```bash ci
curl -s --user minioadmin:minioadmin --aws-sigv4 aws:amz:us-east-1:s3 \
    'http://localhost:9000/registry?list-type=2&prefix=docker/registry/'
```

<!--
```bash ci
curl -s --user minioadmin:minioadmin --aws-sigv4 aws:amz:us-east-1:s3 \
    'http://localhost:9000/registry?list-type=2&prefix=docker/registry/' \
    | grep -q operator
echo "example completed successfully"
```
-->

And that's the magic. A standard kro made multicluster-capable with an
aggregated API server to proxy the objects.
