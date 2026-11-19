package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"testing"

	"github.com/haiven-dev/haiven-cli/internal/demo"
	"github.com/haiven-dev/haiven-cli/internal/result"
)

const demoRevision = "9769859b1044d556da6994df999d17a2e83544bb"

type fakeDemoOpener struct {
	request demo.Request
	result  demo.Result
	err     error
	output  string
}

type fakeDemoBuilder struct {
	request demo.BuildRequest
	result  demo.BuildResult
	err     error
	output  string
}

type fakeDemoRegistrar struct {
	request demo.RegisterRequest
	result  demo.RegisterResult
	err     error
	output  string
}

func (f *fakeDemoOpener) Open(_ context.Context, request demo.Request, stdout, _ io.Writer) (demo.Result, error) {
	f.request = request
	_, _ = io.WriteString(stdout, f.output)
	return f.result, f.err
}

func (f *fakeDemoBuilder) Build(_ context.Context, request demo.BuildRequest, stdout, _ io.Writer) (demo.BuildResult, error) {
	f.request = request
	_, _ = io.WriteString(stdout, f.output)
	return f.result, f.err
}

func (f *fakeDemoRegistrar) Register(_ context.Context, request demo.RegisterRequest, stdout, _ io.Writer) (demo.RegisterResult, error) {
	f.request = request
	_, _ = io.WriteString(stdout, f.output)
	return f.result, f.err
}

func TestDemoOpenDelegatesExactCheckoutToSDKWorkflow(t *testing.T) {
	root := t.TempDir()
	sdkRoot := filepath.Join(root, "sdk")
	apiRoot := filepath.Join(root, "api")
	destination := filepath.Join(root, "assembled")
	opener := &fakeDemoOpener{result: demo.Result{
		SDKRoot: sdkRoot, Revision: demoRevision, Destination: destination,
		Project: filepath.Join(destination, "BossRoom"),
	}}
	got := invocation{args: []string{
		"sdk", "demo", "open", "unity-bossroom",
		"--sdk-root", sdkRoot, "--sdk-revision", demoRevision,
		"--api-root", apiRoot, "--dest", destination,
		"--oauth", "--no-editor", "--unity", filepath.Join(root, "Unity"), "--json",
	}, demoOpener: opener}.run(t)
	if got.exit != 0 || got.stderr != "" {
		t.Fatalf("exit=%d stderr=%q stdout=%s", got.exit, got.stderr, got.stdout)
	}
	if opener.request.SDKRoot != sdkRoot || opener.request.ExpectedRevision != demoRevision || opener.request.APIRoot != apiRoot || opener.request.Destination != destination || opener.request.Backend != "local-api" || !opener.request.OAuth || !opener.request.NoEditor {
		t.Fatalf("request=%+v", opener.request)
	}
	var envelope result.Envelope
	if err := json.Unmarshal([]byte(got.stdout), &envelope); err != nil {
		t.Fatal(err)
	}
	data, ok := envelope.Data.(map[string]any)
	if !envelope.OK || !ok || data["project"] != filepath.Join(destination, "BossRoom") || data["sdk_revision"] != demoRevision {
		t.Fatalf("envelope=%+v stdout=%s", envelope, got.stdout)
	}
}

func TestDemoOpenNoEditorReportsConfigured(t *testing.T) {
	opener := &fakeDemoOpener{result: demo.Result{
		SDKRoot: "/sdk", Revision: demoRevision, Destination: "/demo", Project: "/demo/BossRoom",
	}}
	got := invocation{args: []string{
		"sdk", "demo", "open", "unity-bossroom", "--sdk-root", "sdk",
		"--sdk-revision", demoRevision, "--api-root", "api", "--no-editor",
	}, demoOpener: opener}.run(t)
	if got.exit != 0 || got.stderr != "" || got.stdout != "Configured /demo/BossRoom from SDK "+demoRevision+" at /sdk\n" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", got.exit, got.stdout, got.stderr)
	}
}

func TestDemoOpenVerifiesStandaloneBossRoomWithoutBackend(t *testing.T) {
	opener := &fakeDemoOpener{result: demo.Result{
		SDKRoot: "/BossRoom", Revision: demoRevision, Destination: "/BossRoom",
		Project: "/BossRoom", Standalone: true,
	}}
	got := invocation{args: []string{
		"sdk", "demo", "open", "unity-bossroom", "--sdk-root", "BossRoom",
		"--sdk-revision", demoRevision, "--no-editor",
	}, demoOpener: opener}.run(t)
	if got.exit != 0 || got.stderr != "" || got.stdout != "Verified /BossRoom from checkout "+demoRevision+" at /BossRoom\n" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", got.exit, got.stdout, got.stderr)
	}
	if opener.request.APIRoot != "" || opener.request.Backend != "" {
		t.Fatalf("standalone request set a development backend: %+v", opener.request)
	}
}

func TestDemoOpenRejectsRevisionMismatch(t *testing.T) {
	opener := &fakeDemoOpener{err: errors.Join(demo.ErrRevisionMismatch, errors.New("expected one, found another"))}
	got := invocation{args: []string{
		"sdk", "demo", "open", "unity-bossroom", "--sdk-root", "sdk",
		"--sdk-revision", demoRevision, "--api-root", "api", "--json",
	}, demoOpener: opener}.run(t)
	var envelope result.Envelope
	if err := json.Unmarshal([]byte(got.stdout), &envelope); err != nil {
		t.Fatal(err)
	}
	if got.exit != int(result.ExitIntegrity) || envelope.Diagnostics[0].Code != result.DiagDemoRevisionMismatch {
		t.Fatalf("exit=%d envelope=%+v", got.exit, envelope)
	}
}

func TestDemoOpenKeepsChildOutputOffJSONStdout(t *testing.T) {
	opener := &fakeDemoOpener{output: "sdk child output\n", result: demo.Result{
		SDKRoot: "/sdk", Revision: demoRevision, Destination: "/demo", Project: "/demo/BossRoom",
	}}
	got := invocation{args: []string{
		"sdk", "demo", "open", "unity-bossroom", "--sdk-root", "sdk",
		"--sdk-revision", demoRevision, "--api-root", "api", "--json",
	}, demoOpener: opener}.run(t)
	if got.exit != 0 || got.stderr != "sdk child output\n" {
		t.Fatalf("exit=%d stderr=%q", got.exit, got.stderr)
	}
	var envelope result.Envelope
	if err := json.Unmarshal([]byte(got.stdout), &envelope); err != nil {
		t.Fatalf("stdout was corrupted by child output: %v\n%s", err, got.stdout)
	}
}

func TestDemoBuildDelegatesExactCheckoutAndLinuxTarget(t *testing.T) {
	root := t.TempDir()
	sdkRoot := filepath.Join(root, "sdk")
	destination := filepath.Join(root, "assembled")
	output := filepath.Join(root, "players", "linux")
	player := filepath.Join(output, "BossRoom.x86_64")
	builder := &fakeDemoBuilder{output: "sdk child output\n", result: demo.BuildResult{
		Result: demo.Result{
			SDKRoot: sdkRoot, Revision: demoRevision, Destination: destination,
			Project: filepath.Join(destination, "BossRoom"),
		},
		Target: "linux64", Output: output, Player: player,
	}}
	got := invocation{args: []string{
		"sdk", "demo", "build", "unity-bossroom",
		"--sdk-root", sdkRoot, "--sdk-revision", demoRevision,
		"--dest", destination, "--target", "linux64", "--out", output,
		"--unity", filepath.Join(root, "Unity"), "--timeout", "120", "--json",
	}, demoBuilder: builder}.run(t)
	if got.exit != 0 || got.stderr != "sdk child output\n" {
		t.Fatalf("exit=%d stderr=%q stdout=%s", got.exit, got.stderr, got.stdout)
	}
	if builder.request.SDKRoot != sdkRoot || builder.request.ExpectedRevision != demoRevision || builder.request.Target != "linux64" || builder.request.Output != output || builder.request.Timeout != 120 {
		t.Fatalf("request=%+v", builder.request)
	}
	var envelope result.Envelope
	if err := json.Unmarshal([]byte(got.stdout), &envelope); err != nil {
		t.Fatal(err)
	}
	data, ok := envelope.Data.(map[string]any)
	if !envelope.OK || !ok || data["player"] != player || data["target"] != "linux64" || data["sdk_revision"] != demoRevision {
		t.Fatalf("envelope=%+v stdout=%s", envelope, got.stdout)
	}
}

func TestDemoBuildRejectsInvalidTarget(t *testing.T) {
	got := invocation{args: []string{
		"sdk", "demo", "build", "unity-bossroom",
		"--sdk-root", "sdk", "--sdk-revision", demoRevision, "--target", "webgl", "--json",
	}, demoBuilder: &fakeDemoBuilder{}}.run(t)
	var envelope result.Envelope
	if err := json.Unmarshal([]byte(got.stdout), &envelope); err != nil {
		t.Fatal(err)
	}
	if got.exit != int(result.ExitUsage) || envelope.Diagnostics[0].Code != result.DiagInvalidFlag {
		t.Fatalf("exit=%d envelope=%+v", got.exit, envelope)
	}
}

func TestDemoRegisterDelegatesExactProjectToUnityHub(t *testing.T) {
	root := t.TempDir()
	sdkRoot := filepath.Join(root, "sdk")
	destination := filepath.Join(root, "assembled")
	hubCLI := filepath.Join(root, "bin", "unity")
	registrar := &fakeDemoRegistrar{output: "hub child output\n", result: demo.RegisterResult{
		Result: demo.Result{
			SDKRoot: sdkRoot, Revision: demoRevision, Destination: destination,
			Project: filepath.Join(destination, "BossRoom"),
		},
		AlreadyRegistered: true,
	}}
	got := invocation{args: []string{
		"sdk", "demo", "register", "unity-bossroom",
		"--sdk-root", sdkRoot, "--sdk-revision", demoRevision,
		"--dest", destination, "--hub-cli", hubCLI, "--json",
	}, demoRegistrar: registrar}.run(t)
	if got.exit != 0 || got.stderr != "hub child output\n" {
		t.Fatalf("exit=%d stderr=%q stdout=%s", got.exit, got.stderr, got.stdout)
	}
	if registrar.request.SDKRoot != sdkRoot || registrar.request.ExpectedRevision != demoRevision || registrar.request.Destination != destination || registrar.request.HubCLI != hubCLI {
		t.Fatalf("request=%+v", registrar.request)
	}
	var envelope result.Envelope
	if err := json.Unmarshal([]byte(got.stdout), &envelope); err != nil {
		t.Fatal(err)
	}
	data, ok := envelope.Data.(map[string]any)
	if !envelope.OK || !ok || data["project"] != filepath.Join(destination, "BossRoom") || data["already_registered"] != true {
		t.Fatalf("envelope=%+v stdout=%s", envelope, got.stdout)
	}
}

func TestDemoRegisterExplainsHowToRefreshAnAlreadyOpenHub(t *testing.T) {
	registrar := &fakeDemoRegistrar{result: demo.RegisterResult{Result: demo.Result{
		SDKRoot: "/sdk", Revision: demoRevision, Destination: "/demo", Project: "/demo/BossRoom",
	}}}
	got := invocation{args: []string{
		"sdk", "demo", "register", "unity-bossroom",
		"--sdk-root", "/sdk", "--sdk-revision", demoRevision,
	}, demoRegistrar: registrar}.run(t)
	want := "Registered /demo/BossRoom in Unity Hub\n" +
		"If an already-open Unity Hub does not show the project, close and reopen the Hub; the registration is already saved.\n"
	if got.exit != 0 || got.stderr != "" || got.stdout != want {
		t.Fatalf("exit=%d stdout=%q stderr=%q", got.exit, got.stdout, got.stderr)
	}
}

func TestDemoRegisterRejectsRevisionMismatch(t *testing.T) {
	registrar := &fakeDemoRegistrar{err: errors.Join(demo.ErrRevisionMismatch, errors.New("expected one, found another"))}
	got := invocation{args: []string{
		"sdk", "demo", "register", "unity-bossroom", "--sdk-root", "sdk",
		"--sdk-revision", demoRevision, "--json",
	}, demoRegistrar: registrar}.run(t)
	var envelope result.Envelope
	if err := json.Unmarshal([]byte(got.stdout), &envelope); err != nil {
		t.Fatal(err)
	}
	if got.exit != int(result.ExitIntegrity) || envelope.Diagnostics[0].Code != result.DiagDemoRevisionMismatch {
		t.Fatalf("exit=%d envelope=%+v", got.exit, envelope)
	}
}

func decodeDemoEnvelope(t *testing.T, stdout string) result.Envelope {
	t.Helper()
	var envelope result.Envelope
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("%v\n%s", err, stdout)
	}
	return envelope
}

func TestDemoOpenDelegatesLyraWithEngineAndOmitsBackend(t *testing.T) {
	root := t.TempDir()
	sdkRoot := filepath.Join(root, "sdk")
	engine := filepath.Join(root, "UE_5.8")
	destination := filepath.Join(root, "lyra-demo")
	opener := &fakeDemoOpener{result: demo.Result{
		SDKRoot: sdkRoot, Revision: demoRevision, Destination: destination, Project: filepath.Join(destination, "Lyra"),
	}}
	got := invocation{args: []string{
		"sdk", "demo", "open", "unreal-lyra", "--sdk-root", sdkRoot, "--sdk-revision", demoRevision,
		"--engine", engine, "--dest", destination, "--item", "tag/hero=ID_Pistol", "--json",
	}, demoOpener: opener}.run(t)
	if got.exit != 0 || got.stderr != "" {
		t.Fatalf("exit=%d stderr=%q stdout=%s", got.exit, got.stderr, got.stdout)
	}
	if opener.request.Demo != "unreal-lyra" || opener.request.Engine != engine || opener.request.Backend != "" || len(opener.request.Items) != 1 {
		t.Fatalf("request=%+v", opener.request)
	}
	data, ok := decodeDemoEnvelope(t, got.stdout).Data.(map[string]any)
	if !ok || data["demo"] != "unreal-lyra" || data["project"] != filepath.Join(destination, "Lyra") {
		t.Fatalf("data=%+v", data)
	}
	if _, present := data["backend"]; present {
		t.Fatalf("a Lyra result reported a Boss Room backend: %+v", data)
	}
}

func TestDemoOpenLyraNeedsEngineUnlessNoEditor(t *testing.T) {
	got := invocation{args: []string{
		"sdk", "demo", "open", "unreal-lyra", "--sdk-root", "sdk", "--sdk-revision", demoRevision, "--json",
	}, demoOpener: &fakeDemoOpener{}}.run(t)
	if envelope := decodeDemoEnvelope(t, got.stdout); got.exit != int(result.ExitUsage) || envelope.Diagnostics[0].Code != result.DiagMissingArgument {
		t.Fatalf("exit=%d envelope=%+v", got.exit, envelope)
	}
	opener := &fakeDemoOpener{result: demo.Result{SDKRoot: "/sdk", Revision: demoRevision, Destination: "/demo", Project: "/demo/Lyra"}}
	got = invocation{args: []string{
		"sdk", "demo", "open", "unreal-lyra", "--sdk-root", "sdk", "--sdk-revision", demoRevision, "--no-editor",
	}, demoOpener: opener}.run(t)
	if got.exit != 0 || got.stdout != "Configured "+filepath.Join("/demo/Lyra", "Lyra.uproject")+" from SDK "+demoRevision+" at /sdk\n" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", got.exit, got.stdout, got.stderr)
	}
}

func TestDemoOpenGodotDelegatesExactCheckout(t *testing.T) {
	opener := &fakeDemoOpener{result: demo.Result{
		SDKRoot: "/sdk", Revision: demoRevision, Destination: "/sdk/demos/godot", Project: "/sdk/demos/godot",
	}}
	got := invocation{args: []string{
		"sdk", "demo", "open", "godot-demo", "--sdk-root", "/sdk", "--sdk-revision", demoRevision, "--godot", "godot4",
	}, demoOpener: opener}.run(t)
	if got.exit != 0 || got.stdout != "Opened /sdk/demos/godot from SDK "+demoRevision+" at /sdk\n" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", got.exit, got.stdout, got.stderr)
	}
	if opener.request.Demo != "godot-demo" || opener.request.Godot != "godot4" || opener.request.APIRoot != "" {
		t.Fatalf("request=%+v", opener.request)
	}
}

func TestDemoRejectsFlagsOfAnotherEngine(t *testing.T) {
	for _, args := range [][]string{
		{"sdk", "demo", "open", "godot-demo", "--sdk-root", "sdk", "--sdk-revision", demoRevision, "--unity", "Unity", "--json"},
		{"sdk", "demo", "open", "unity-bossroom", "--sdk-root", "sdk", "--sdk-revision", demoRevision, "--api-root", "api", "--engine", "UE", "--json"},
		{"sdk", "demo", "build", "unreal-lyra", "--sdk-root", "sdk", "--sdk-revision", demoRevision, "--target", "linux64", "--timeout", "5", "--json"},
	} {
		opener, builder := &fakeDemoOpener{}, &fakeDemoBuilder{}
		got := invocation{args: args, demoOpener: opener, demoBuilder: builder}.run(t)
		envelope := decodeDemoEnvelope(t, got.stdout)
		if got.exit != int(result.ExitUsage) || envelope.Diagnostics[0].Code != result.DiagInvalidFlag || opener.request.SDKRoot != "" || builder.request.SDKRoot != "" {
			t.Fatalf("args=%v exit=%d envelope=%+v", args, got.exit, envelope)
		}
	}
}

func TestDemoBuildDelegatesLyraAndReportsPackageFolder(t *testing.T) {
	builder := &fakeDemoBuilder{result: demo.BuildResult{
		Result: demo.Result{SDKRoot: "/sdk", Revision: demoRevision, Destination: "/demo", Project: "/demo/Lyra"},
		Target: "linux64", Output: "/demo/build/Linux",
	}}
	got := invocation{args: []string{
		"sdk", "demo", "build", "unreal-lyra", "--sdk-root", "/sdk", "--sdk-revision", demoRevision,
		"--target", "linux64", "--content", "/fab/Lyra", "--json",
	}, demoBuilder: builder}.run(t)
	if got.exit != 0 {
		t.Fatalf("exit=%d stdout=%s", got.exit, got.stdout)
	}
	if builder.request.Demo != "unreal-lyra" || builder.request.Content != "/fab/Lyra" || builder.request.Target != "linux64" {
		t.Fatalf("request=%+v", builder.request)
	}
	data, _ := decodeDemoEnvelope(t, got.stdout).Data.(map[string]any)
	if _, present := data["player"]; present || data["output"] != "/demo/build/Linux" {
		t.Fatalf("data=%+v", data)
	}
}

func TestDemoBuildLyraEditorOnlySaysNoClientWasPackaged(t *testing.T) {
	builder := &fakeDemoBuilder{result: demo.BuildResult{
		Result: demo.Result{SDKRoot: "/sdk", Revision: demoRevision, Destination: "/demo", Project: "/demo/Lyra"},
		Target: "linux64",
	}}
	got := invocation{args: []string{
		"sdk", "demo", "build", "unreal-lyra", "--sdk-root", "/sdk", "--sdk-revision", demoRevision,
		"--target", "linux64", "--editor-only",
	}, demoBuilder: builder}.run(t)
	want := "Built the editor for /demo/Lyra from SDK " + demoRevision + " at /sdk; no client was packaged\n"
	if got.exit != 0 || got.stdout != want || !builder.request.EditorOnly {
		t.Fatalf("exit=%d stdout=%q request=%+v", got.exit, got.stdout, builder.request)
	}
}

func TestDemoRefusesOperationsAnEngineDoesNotHave(t *testing.T) {
	for _, args := range [][]string{
		{"sdk", "demo", "build", "godot-demo", "--sdk-root", "sdk", "--sdk-revision", demoRevision, "--target", "linux64", "--json"},
		{"sdk", "demo", "register", "unreal-lyra", "--sdk-root", "sdk", "--sdk-revision", demoRevision, "--json"},
		{"sdk", "demo", "register", "godot-demo", "--sdk-root", "sdk", "--sdk-revision", demoRevision, "--json"},
		{"sdk", "demo", "open", "roblox-obby", "--sdk-root", "sdk", "--sdk-revision", demoRevision, "--json"},
	} {
		builder, registrar, opener := &fakeDemoBuilder{}, &fakeDemoRegistrar{}, &fakeDemoOpener{}
		got := invocation{args: args, demoBuilder: builder, demoRegistrar: registrar, demoOpener: opener}.run(t)
		envelope := decodeDemoEnvelope(t, got.stdout)
		if got.exit != int(result.ExitUsage) || envelope.Diagnostics[0].Code != result.DiagDemoUnknown || builder.request.SDKRoot != "" || registrar.request.SDKRoot != "" || opener.request.SDKRoot != "" {
			t.Fatalf("args=%v exit=%d envelope=%+v", args, got.exit, envelope)
		}
	}
}
