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
