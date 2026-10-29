# oiax

A small Kubernetes operator, built in the open as a course in how operators work. You describe a web page in a `Course`, and oiax runs it: a ConfigMap holds the page, a Deployment serves it, a Service puts it on the network, and the operator keeps all three the way the Course says.

It's deliberately small. The interesting part isn't the web page; it's the loop that keeps it there.

## A Course

```yaml
apiVersion: sail.oiax.example.com/v1alpha1
kind: Course
metadata:
  name: course-sample
spec:
  message: Fair winds from the sample Course
  replicas: 2
```

- **`message`** is the text the page shows. It's required, from 1 to 1024 characters.
- **`title`** is an optional heading shown above the message, up to 200 characters. A Course without one serves exactly the page it did before titles existed.
- **`replicas`** is how many pods serve it, from 0 to 10. It defaults to 1.
- **`image`** is the web server. It defaults to `nginxinc/nginx-unprivileged:1.30-alpine`, and any image you swap in has to listen on 8080 and serve `/usr/share/nginx/html`.

The API server enforces those limits, so `replicas: 11` is refused before the operator ever sees it.

## What the operator does

Every time a Course or anything it owns changes, the operator compares what's there with what the Course asks for and fixes the difference. It doesn't track what changed, so it doesn't matter which change woke it or whether it runs twice.

- **It owns what it makes.** Delete the Deployment and it comes straight back; delete the Course and Kubernetes removes all three.
- **A new message rolls the pods.** A hash of the message sits on the pod template, so changing the text starts a rollout instead of waiting for pods to restart on their own.
- **It leaves your annotations alone.** It sets its own key on the pod template and keeps the rest, so `kubectl rollout restart` works on a Course's Deployment.
- **It reports back.** `kubectl get courses` shows how many replicas are ready, and an `Available` condition turns `True` once every replica asked for is serving.

## Run the tests

You need Go (the version is in `go.mod`) and `make`.

```bash
make check
```

That's the same gate every commit passes: generated code, the linter, and the tests against a local API server from `envtest`. It doesn't touch any cluster.

`make test-e2e` builds the operator's image and runs it on a throwaway `kind` cluster, which needs Docker and `kind` too.

## Try it on kind

You need Docker, `kind`, `kubectl` and Helm.

```bash
make demo
```

That creates a `kind` cluster called `oiax-demo`, builds the operator's image and loads it into the cluster, installs the operator from the Helm chart in `dist/chart`, applies the sample Course and waits for it to report `Available`. It ends with `kubectl get courses`. Every command names the cluster's own context, `kind-oiax-demo`, so it never touches whichever cluster your `kubectl` normally points at.

To see the page:

```bash
kubectl --context kind-oiax-demo port-forward service/course-sample 8080:80
```

Then open <http://localhost:8080>. `make demo` is safe to run again, and `make demo-clean` deletes the cluster.

The chart installs the CRD as a template, so `helm upgrade` changes it too, and `helm uninstall` leaves it in place, because deleting a CRD deletes every Course with it.

## When things break

The operator remembers nothing between runs. Each time, it reads the Course and makes the cluster match it, so it doesn't matter what broke or how long the operator was gone. Three ways to see that on the `make demo` cluster. First leave a watch running in a second terminal, where every status change shows up as a new line:

```bash
kubectl --context kind-oiax-demo get courses -w --output-watch-events
```

Watch that rather than `kubectl wait`. Right after a break, `kubectl wait --for=condition=Available` can still read the old `True` and return before the operator has noticed anything. The times below are from one run on a laptop; yours will differ a little.

**Kill the operator.**

```bash
kubectl --context kind-oiax-demo -n oiax-system delete pod -l control-plane=controller-manager
```

A new pod is ready about 11 seconds later and takes the leader lease 5 to 7 seconds after that; `kubectl --context kind-oiax-demo -n oiax-system get lease` names the holder. Only one copy of the operator may act at a time, so a new pod does nothing until it holds the lease. The watch stays quiet, because nothing is wrong with the Course.

**Delete what it owns.**

```bash
kubectl --context kind-oiax-demo delete deployment course-sample
```

The Deployment is back a fraction of a second later, and in the watch the Course goes `False`, then `True` again within about a second as the pods come up. The Course owns the Deployment, so deleting it wakes the operator, which finds a Course with no Deployment and makes one.

**Stop the operator, then break things.**

```bash
kubectl --context kind-oiax-demo -n oiax-system scale deploy/oiax-controller-manager --replicas=0
kubectl --context kind-oiax-demo delete deployment course-sample
kubectl --context kind-oiax-demo patch course course-sample --type=merge -p '{"spec":{"message":"Changed while nobody was steering"}}'
```

Nothing brings the Deployment back, and the watch still says `2   2   True`. That's the part to remember: status is the operator's last report, not a live reading, and with the operator gone nobody updates it. The Course does carry a clue. Its `metadata.generation` went up with the patch and its `status.observedGeneration` didn't, so the status describes an older spec than the one you're reading:

```bash
kubectl --context kind-oiax-demo get course course-sample -o jsonpath='{.metadata.generation} {.status.observedGeneration}{"\n"}'
```

On a fresh `make demo` that prints `2 1`. Start the operator again:

```bash
kubectl --context kind-oiax-demo -n oiax-system scale deploy/oiax-controller-manager --replicas=1
```

About 17 seconds later, once it holds the lease, the Deployment is back, the Course goes `False` and then `True`, and the page has the new message:

```bash
kubectl --context kind-oiax-demo get configmap course-sample -o jsonpath='{.data}'
```

The operator never saw the message change, and it didn't need to. It reads the Course as it is now.

## Let Argo CD install it

```bash
make argocd-demo
```

This time nothing is installed by hand. It creates a second `kind` cluster, `oiax-argocd`, so it never fights the Helm release from `make demo`, loads the operator's image, and installs Argo CD's core (the controllers, without the web UI). Then it applies `deploy/argocd/applications.yaml`: a project that may read only this repository and write only to the two namespaces it needs, and two Applications in it. One installs the operator from `dist/chart`, the other the sample Course from `config/samples`. Both are read from git, not from your checkout, and both sync on their own and put back anything changed by hand: `kubectl patch` the sample's replicas and Argo CD sets them back within a couple of seconds. It ends when the Course is `Available` and both Applications are `Healthy`.

Argo CD follows `main` in this repository. To follow your fork or a branch:

```bash
make argocd-demo REPO_URL=https://github.com/you/oiax.git REVISION=my-branch
```

A change has to be pushed before Argo CD can see it. The sample Course's first syncs may fail with no such kind, because the CRD arrives with the other Application; it retries on its own.

`make argocd-clean` deletes the cluster.

## Planned, not built yet

It gets its own section here once it works:

- an upgrade drill that adds a field to the Course without breaking the ones already running.

## Contributing

[CONTRIBUTING.md](CONTRIBUTING.md) has the short version: `make check` green, one concern per pull request, and a test that fails before your change. Security reports go through [SECURITY.md](SECURITY.md), not a public issue.

## License

Apache-2.0. See [LICENSE](LICENSE).
