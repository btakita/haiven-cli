# Open the Haiven Boss Room sample

Boss Room is a standalone Unity 6 project with the Haiven SDK and demo
packages already embedded. It follows the Unity Companion License.
Choose a reviewed release in the [Boss Room repository][boss-room]
when the public preview is available. Resolve the release to its full Git
commit and save it as `BOSSROOM_COMMIT` for the shell commands below.

[boss-room]: https://github.com/haiven-dev/haiven-sdk-unity-bossroom

## Plan

Install Git, Git LFS, and Unity **6000.0.52f1** with an activated license.
Choose a destination where the `BossRoom` folder does not exist yet.

```haiven-manual-step
{
  "id": "prerequisites",
  "operation": "plan",
  "native_tool": "Git, Git LFS, and Unity Hub",
  "action": "Confirm the Boss Room release commit, Git LFS availability, and the pinned Unity Editor.",
  "preconditions": [
    "The reviewed release and exact commit are known."
  ],
  "expected_outcome": "The developer has the tools and an empty destination for the selected release.",
  "evidence": [
    "The release commit, Editor version, and planned checkout path are recorded."
  ],
  "requires_haiven_cli": false,
  "mutations": []
}
```

## Apply

Set `BOSSROOM_COMMIT` to the reviewed 40-character commit SHA. Run these
commands in a POSIX shell, such as Git Bash on Windows.

```sh
git clone --no-checkout https://github.com/haiven-dev/haiven-sdk-unity-bossroom.git BossRoom &&
  git -C BossRoom lfs install --local &&
  git -C BossRoom checkout --detach "$BOSSROOM_COMMIT" &&
  git -C BossRoom lfs pull &&
  test "$(git -C BossRoom rev-parse HEAD)" = "$BOSSROOM_COMMIT"
```

The checkout includes both Haiven packages at `Packages/com.haiven.sdk` and
`Packages/com.haiven.demo.bossroom`. Read the project README for its version
and the release's game setup.

```haiven-manual-step
{
  "id": "acquire-boss-room",
  "operation": "apply",
  "native_tool": "Git and Git LFS",
  "action": "Clone the standalone demo, check out the exact reviewed commit, and hydrate Git LFS files.",
  "preconditions": [
    "Git and Git LFS are available and the destination is new."
  ],
  "expected_outcome": "BossRoom contains the selected release's complete worktree.",
  "evidence": [
    "HEAD equals the recorded release commit."
  ],
  "requires_haiven_cli": false,
  "mutations": [
    "BossRoom"
  ]
}
```

```haiven-manual-step
{
  "id": "check-pinned-packages",
  "operation": "apply",
  "native_tool": "Unity project package manifests",
  "action": "Confirm the two embedded Haiven packages and the release's tracked Packages/manifest.json.",
  "preconditions": [
    "The standalone project has been checked out."
  ],
  "expected_outcome": "Package Manager can resolve the packages included in the selected demo release.",
  "evidence": [
    "Both package directories have their package.json files at the selected commit."
  ],
  "requires_haiven_cli": false,
  "mutations": []
}
```

Set the demo's public integration values in the process environment before
launching Unity. Use the API, realtime, and browser sign-in endpoints for your
preview environment. Register `http://127.0.0.1:47615/haiven/callback` on your
game integration. Set `HAIVEN_DEMO_BASE_URL`, `HAIVEN_DEMO_REALTIME_URL`,
`HAIVEN_DEMO_SIGN_IN_URL`, `HAIVEN_DEMO_APP_ID`, and
`HAIVEN_DEMO_INTEGRATION_ID`. For chat and rewards, set
`HAIVEN_DEMO_CHANNEL_ID` and `HAIVEN_DEMO_REWARD_REF`. Keep integration keys
and player tokens out of project files. Player sign-in runs in the browser.

Open `BossRoom` using Unity 6000.0.52f1 with that environment. Open
`Assets/Scenes/Startup.unity` and allow import and compilation to finish.

```haiven-manual-step
{
  "id": "configure-demo",
  "operation": "apply",
  "native_tool": "Haiven game integration and Unity process environment",
  "action": "Set public app and integration values and register the exact loopback redirect.",
  "preconditions": [
    "The preview environment and an authorized app integration are available."
  ],
  "expected_outcome": "The demo can start browser sign-in with the configured public values.",
  "evidence": [
    "The app integration lists the expected redirect and project files contain no credentials."
  ],
  "requires_haiven_cli": false,
  "mutations": []
}
```

```haiven-manual-step
{
  "id": "open-in-unity",
  "operation": "apply",
  "native_tool": "Unity Editor 6000.0.52f1",
  "action": "Open the pinned project and load the Startup scene after import succeeds.",
  "preconditions": [
    "The checkout is hydrated and the Unity process inherits the demo configuration."
  ],
  "expected_outcome": "The Startup scene opens in the matching Editor.",
  "evidence": [
    "The Unity Console reports no package or compilation error."
  ],
  "requires_haiven_cli": false,
  "mutations": []
}
```

## Verify

Run `git -C BossRoom lfs fsck --dry-run --objects HEAD` and compare
`git -C BossRoom rev-parse HEAD` with the reviewed commit. In the Startup
scene, enter Play Mode and complete browser sign-in. Verify that the player
connects and can exercise the lobby and the features enabled for the app.
Keep the release commit, Editor version, and observed results with your
test record.

```haiven-manual-step
{
  "id": "verify-checkout",
  "operation": "verify",
  "native_tool": "Git and Git LFS",
  "action": "Compare the checkout to the reviewed commit and verify the required LFS objects.",
  "preconditions": [
    "The standalone release is present locally."
  ],
  "expected_outcome": "The worktree has the intended commit and its required assets.",
  "evidence": [
    "Git reports the selected SHA and the dry-run Git LFS check passes."
  ],
  "requires_haiven_cli": false,
  "mutations": []
}
```

```haiven-manual-step
{
  "id": "play-sample",
  "operation": "verify",
  "native_tool": "Unity Editor Play Mode",
  "action": "Start the Startup scene, sign in through the browser, and exercise the enabled demo flows.",
  "preconditions": [
    "The correct Editor imported the project and the preview integration is configured."
  ],
  "expected_outcome": "The sample connects and its enabled lobby, chat, or reward flows can be tested.",
  "evidence": [
    "Record the observed in-game outcome and any Unity Console errors."
  ],
  "requires_haiven_cli": false,
  "mutations": []
}
```

```haiven-manual-step
{
  "id": "record-evidence",
  "operation": "verify",
  "native_tool": "Test notes",
  "action": "Record the release commit, Editor version, and the outcome of each demo check.",
  "preconditions": [
    "Checkout and Play Mode checks have been attempted."
  ],
  "expected_outcome": "The test record identifies the exact sample and preview environment.",
  "evidence": [
    "The record contains the commit, Unity version, and observed results without credentials."
  ],
  "requires_haiven_cli": false,
  "mutations": []
}
```
