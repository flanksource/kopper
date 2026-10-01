# kopper

A library for keeping all the kubernetes operator logic

## Persisted resources that are not ready

An `OnUpsertFunc` can return `kopper.NotReady` after storing a resource that
cannot take effect yet:

```go
return kopper.NotReady("ScopeNotFound", "Scope %s/%s does not exist", namespace, scopeName)
```

Kopper sets `Ready=False` with the supplied CamelCase reason and formatted
message, and updates `status.observedGeneration`. An empty reason defaults to
`Invalid`, including when returning `NotReadyError` directly. The resource must implement
`StatusConditioner` and `ObservedGenerationSetter` to expose these fields.
Status writes still use `StatusPatchGenerator` when implemented.

This is not a persistence failure: Kopper logs at verbosity 2 and returns no
reconcile error unless the status write fails. A Warning event is emitted only
when the Ready condition's status or reason changes, not for message-only updates.
Errors wrapping `*kopper.NotReadyError` are also recognized.

To retain an existing error for `errors.Is` and `errors.As`, use `%w`:

```go
return kopper.NotReady("ScopeNotFound", "invalid role: %w", err)
```

Alternatively, set `Err` on `NotReadyError`. Its text becomes the status message
unless an explicit `Message` overrides it; the underlying error is preserved either way.

The helper does not schedule a retry. To retry after a delay, return the type directly:

```go
return &kopper.NotReadyError{
    Reason:       "ScopeNotFound",
    Message:      "Waiting for the referenced scope",
    RequeueAfter: 30 * time.Second,
}
```

Returning `nil` on a later upsert restores `Ready=True` with reason `Synced`.
Ordinary errors, unique-constraint handling, and deletion behavior are unchanged.

## Reconcile after a dependency changes

Keep the reconciler returned by `SetupReconciler` and enqueue affected resources
when a dependency is stored or deleted:

```go
roles, err := kopper.SetupReconciler(ctx, mgr, persistRole, deleteRole, deleteStaleRole, "roles.example.com")
if err != nil {
    return err
}
roles.Enqueue("staging", "operators")
```

`Enqueue` does not change the resource's spec or generation. After setup it is safe
to call from concurrent goroutines, does not wait for queue capacity, and retains
requests made before the manager starts. The controller workqueue deduplicates
pending requests; a request made during reconciliation schedules another pass.
Missing resources are ignored. Requests are in memory only and are not processed
after the manager stops.

Use `Resync` when a dependency change affects all resources of a kind, or a
namespace/label-selected subset:

```go
if err := roles.Resync(ctx, client.InNamespace("staging"), client.MatchingLabels{"team": "sre"}); err != nil {
    return err
}
return roles.Resync(ctx)
```

`Resync` lists resources and calls `Enqueue` for each. It returns list and context
cancellation errors, or an error if the reconciler has not been set up. Listing
can wait for the API/cache, but enqueueing does not wait for queue capacity.
Cancellation does not remove requests already queued.
