# kopper

A library for keeping all the kubernetes operator logic

## Ready condition

For resources that implement `StatusConditioner`, kopper keeps a `Ready` condition:

| Outcome of `OnUpsertFunc`          | Ready   | Reason                  | Retried                |
| ---------------------------------- | ------- | ----------------------- | ---------------------- |
| `nil`                              | `True`  | `Synced`                | -                      |
| `kopper.NewConditionError(r, err)` | `False` | `r` (default `Invalid`) | No                     |
| any other error                    | `False` | `PersistFailed`         | Yes, after 2 minutes   |

Return a `ConditionError` when a resource can't take effect as written, e.g. it fails validation
or references something that doesn't exist:

```go
if !rlsEnabled {
    return kopper.NewConditionError("RowLevelSecurityRequired", errors.New("row-level security is disabled"))
}
```

## Re-evaluating resources

A resource whose validity depends on state outside of Kubernetes can be re-evaluated without being re-applied:

```go
reconciler, _ := kopper.SetupReconciler(ctx, mgr, onUpsert, onDelete, onConflict, "scope.mission-control.flanksource.com")

// when that state changes
reconciler.Resync(ctx)                        // every resource of the kind
reconciler.Resync(ctx, client.InNamespace(ns)) // narrowed with list options
reconciler.Enqueue(ctx, namespace, name)       // a single resource
```
