# Integrate Haiven into an existing Unity project

This guide is the native, CLI-independent path for adding one immutable Haiven
Unity SDK revision to a game you already own. It supports Unity 2022.3 or newer
on Linux, macOS, and Windows. The release qualification pin is 2022.3.62f2;
2022.3 is the compatibility floor.

The integration never asks for an integration key, bearer, populated environment
file, password, or other credential. Player builds use browser handoff and the
resulting player credential. Server integration keys stay in an approved private
server-side secret channel and never belong in a Unity project.

## Plan

Before changing anything, resolve the reviewed release in the standalone
[Unity SDK repository](https://github.com/haiven-dev/haiven-sdk-unity) to its full
40-character lowercase Git commit. Use the absolute root of the existing
Unity project. It must already contain `Assets`, `Packages/manifest.json`, and
`ProjectSettings/ProjectVersion.txt`. Review local changes to the two package
files and stop if the intended edit would overlap one.

```haiven-manual-step
{
  "id": "inspect-project",
  "operation": "plan",
  "native_tool": "Unity project files and Git",
  "action": "Confirm the caller-owned project, Unity 2022.3-or-newer version, full SDK commit, current package files, and clean overlap before mutation.",
  "preconditions": ["The project already exists and is owned by the caller."],
  "expected_outcome": "The exact package URL and every automated or manual effect are known before any file changes.",
  "evidence": ["ProjectVersion.txt records Unity 2022.3 or newer and the selected SDK revision is 40 lowercase hexadecimal characters."],
  "requires_haiven_cli": false,
  "mutations": []
}
```

## Apply

Edit only the `dependencies` object in `Packages/manifest.json`, preserving all
other project content. Set `com.haiven.sdk` to this URL, replacing `<commit>`
with the selected full commit:

```json
"com.haiven.sdk": "https://github.com/haiven-dev/haiven-sdk-unity.git?path=/com.haiven.sdk#<commit>"
```

An identical rerun is a no-op. If `Packages/manifest.json` or
`Packages/packages-lock.json` has an overlapping uncommitted edit and the
manifest still needs changing, stop and resolve it instead of overwriting it.

```haiven-manual-step
{
  "id": "pin-sdk-package",
  "operation": "apply",
  "native_tool": "Unity Package Manager manifest",
  "action": "Pin com.haiven.sdk in Packages/manifest.json to the Git UPM URL with the full immutable SDK commit.",
  "preconditions": ["The plan was reviewed and overlapping package-file changes are clean."],
  "expected_outcome": "Only the com.haiven.sdk dependency value changes; an identical pin is unchanged.",
  "evidence": ["Packages/manifest.json contains the exact Git UPM URL and retains every unrelated dependency and root field."],
  "requires_haiven_cli": false,
  "mutations": ["Packages/manifest.json"]
}
```

Open this exact project in the Unity Editor and wait for Package Manager import
and script compilation to finish. Do not copy a package directory into Assets
and do not replace the Git pin with a mutable branch or tag.

```haiven-manual-step
{
  "id": "import-sdk-package",
  "operation": "apply",
  "native_tool": "Unity Editor Package Manager",
  "action": "Open the exact project, allow Unity to resolve and import com.haiven.sdk, and inspect the Console before continuing.",
  "preconditions": ["Packages/manifest.json has the exact immutable Git UPM pin."],
  "expected_outcome": "Unity records the Git package in Packages/packages-lock.json and compilation finishes without Haiven package errors.",
  "evidence": ["The resolved lock entry has source git and the exact requested version URL; the Editor Console has no Haiven compile error."],
  "requires_haiven_cli": false,
  "mutations": ["Packages/packages-lock.json"]
}
```

Wire the SDK through game-owned code or scenes using the package README. This
step is intentionally not automated: the runner cannot safely infer your scene,
assembly, lifetime, sign-in UX, or networking architecture. Keep keys and bearer
tokens out of source, scenes, assets, settings, environment files, and logs.

```haiven-manual-step
{
  "id": "wire-sdk",
  "operation": "apply",
  "native_tool": "Unity Editor and caller-owned C#",
  "action": "Add the game-specific Haiven lifetime, handoff, REST, and realtime wiring through normal caller-owned code and scenes without embedding credentials.",
  "preconditions": ["The package imported successfully and its README is available in Package Manager."],
  "expected_outcome": "The game owns an explicit, reviewable integration and no credential is persisted in project content.",
  "evidence": ["A project owner reviews the code/scene wiring and secret scan; a native test or play session exercises the intended path."],
  "requires_haiven_cli": false,
  "mutations": []
}
```

## Verify

Read both package files without changing them. The manifest value must be the
exact requested URL. In `Packages/packages-lock.json`, the
`dependencies.com.haiven.sdk` object must have `source` equal to `git` and
`version` equal to that same URL. A missing entry means the Editor import is
still pending; a different source or version is drift.

```haiven-manual-step
{
  "id": "verify-package-lock",
  "operation": "verify",
  "native_tool": "Unity Package Manager lock",
  "action": "Compare the manifest and resolved package-lock entry with the full requested Haiven SDK commit.",
  "preconditions": ["Unity finished importing the project."],
  "expected_outcome": "Both files identify the same immutable Git UPM URL and package-lock reports source git.",
  "evidence": ["Packages/manifest.json and Packages/packages-lock.json resolve com.haiven.sdk to the exact requested commit."],
  "requires_haiven_cli": false,
  "mutations": []
}
```

Finally, inspect and exercise the game-specific wiring in the native Editor.
Package resolution alone is never proof that the game is integrated.

```haiven-manual-step
{
  "id": "verify-code-wiring",
  "operation": "verify",
  "native_tool": "Unity Editor and project-native tests",
  "action": "Review and exercise the caller-owned SDK wiring, including its credential-free configuration boundary.",
  "preconditions": ["The exact package lock is resolved and the game-specific wiring is present."],
  "expected_outcome": "The intended Haiven path works in the caller's game and no integration key, bearer, populated environment file, or credential is persisted.",
  "evidence": ["Project-native test or play evidence plus a review of the caller-owned wiring and secret boundary."],
  "requires_haiven_cli": false,
  "mutations": []
}
```

The optional runner automates only the safe package-manifest edit and lock-file
inspection. Apply and verify therefore return `manual-required` while Editor
import or game-specific wiring remains pending; they never turn package presence
into a false integration success.
