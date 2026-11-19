# Unreal Lyra starter

This guide is the normative onboarding path. The `haiven` CLI is optional;
follow these steps directly with the SDK tooling and Unreal's normal tools.

```haiven-manual-step
{"id":"unreal-lyra-plan","operation":"plan","native_tool":"Git, Epic-linked GitHub access, and Unreal Engine","action":"Confirm Epic-linked source access, the required Unreal Engine version, and whether Lyra content is already available under Epic's terms.","preconditions":["The SDK checkout is trusted","A writable parent directory is available"],"expected_outcome":"Source and optional content prerequisites are understood before files are written.","evidence":["Record the engine, SDK, and upstream revisions and whether content is available"],"requires_haiven_cli":false,"mutations":[]}
```

```haiven-manual-step
{"id":"unreal-lyra-apply","operation":"apply","native_tool":"haiven-lyra SDK tool","action":"Run the SDK-owned Lyra assembler into an absent or empty LyraStarterGame directory and keep the emitted lock evidence; do not copy or republish Epic content.","preconditions":["The plan prerequisites passed","The destination is absent or empty"],"expected_outcome":"LyraStarterGame contains the pinned source integration and Haiven project plugins; content status remains explicit.","evidence":["Retain the assembler result and starter lock","Review the changed paths before committing"],"requires_haiven_cli":false,"mutations":["LyraStarterGame"]}
```

```haiven-manual-step
{"id":"unreal-lyra-verify","operation":"verify","native_tool":"Unreal project-file tools and Unreal Editor","action":"Generate project files, build the supported editor target, open LyraStarterGame, and run the credentials-free smoke path when required content is present.","preconditions":["Source assembly completed","The required Unreal Engine is installed"],"expected_outcome":"The editor target builds; when content is present, the project opens and the Haiven smoke path completes without credentials.","evidence":["Record the engine version, build result, content status, and smoke result"],"requires_haiven_cli":false,"mutations":[]}
```
