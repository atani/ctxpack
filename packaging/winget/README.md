# winget packaging

CtxPack releases include Windows ZIP artifacts and a generated winget manifest bundle.

To publish a new version to winget after a GitHub release is created:

1. Download `ctxpack_<version>_winget_manifests.zip` from the release.
2. Unzip it into a local clone of `microsoft/winget-pkgs` under `manifests/a/atani/ctxpack/<version>/`.
3. Validate and submit:

```powershell
winget validate manifests\a\atani\ctxpack\<version>\
wingetcreate submit manifests\a\atani\ctxpack\<version>\
```

Once Microsoft merges the community manifest, users can install with:

```powershell
winget install atani.ctxpack
```
