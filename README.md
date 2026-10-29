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

## Planned, not built yet

Each gets its own section here once it works:

- a Helm chart and a `make demo` that goes from a clone to a Course serving its page on `kind`,
- Argo CD syncing that chart from this repository,
- an upgrade drill that adds a field to the Course without breaking the ones already running,
- a failure drill that kills the operator and deletes what it owns, and watches both come back.

## Contributing

[CONTRIBUTING.md](CONTRIBUTING.md) has the short version: `make check` green, one concern per pull request, and a test that fails before your change. Security reports go through [SECURITY.md](SECURITY.md), not a public issue.

## License

Apache-2.0. See [LICENSE](LICENSE).
