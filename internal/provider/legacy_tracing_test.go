package provider

import (
	"context"
	"testing"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness/host"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/inventory"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/provider/framework"
	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/metadata"
)

const legacyTraceparent = "00-1234567890abcdef1234567890abcdef-1234567890abcdef-01"

func TestLegacyResourceTracing(t *testing.T) {
	for _, transport := range []string{"environment", "metadata"} {
		t.Run(transport, func(t *testing.T) {
			ctx := t.Context()
			t.Setenv("TRACEPARENT", legacyTraceparent)
			if transport == "metadata" {
				// A different environment parent makes metadata precedence observable.
				t.Setenv("TRACEPARENT", "00-abcdef1234567890abcdef1234567890-abcdef1234567890-01")
				ctx = metadata.NewIncomingContext(ctx, metadata.Pairs("traceparent", legacyTraceparent))
			}
			parent := propagation.TraceContext{}.Extract(ctx, propagation.MapCarrier{"traceparent": legacyTraceparent})

			for _, tc := range []struct {
				name      string
				failPhase string
				warn      bool
				retry     bool
				skipped   bool
				wantSteps int
			}{
				{name: "success", wantSteps: 3},
				{name: "before failure", failPhase: "before", wantSteps: 2},
				{name: "assessment failure", failPhase: "assessment", wantSteps: 3},
				{name: "after failure", failPhase: "after", wantSteps: 3},
				{name: "warning", failPhase: "assessment", warn: true, wantSteps: 3},
				{name: "retry", failPhase: "assessment", retry: true, wantSteps: 4},
				{name: "skipped", skipped: true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					recorder := tracetest.NewSpanRecorder()
					tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
					previous := otel.GetTracerProvider()
					otel.SetTracerProvider(tp)
					t.Cleanup(func() {
						otel.SetTracerProvider(previous)
						if err := tp.Shutdown(context.WithoutCancel(t.Context())); err != nil {
							t.Error(err)
						}
					})

					seed := t.TempDir()
					inv, err := inventory.NewInventory(seed)
					if err != nil {
						t.Fatal(err)
					}
					if err := inv.AddHarness(ctx, "harness-id"); err != nil {
						t.Fatal(err)
					}
					skipReason := ""
					if tc.skipped {
						skipReason = "excluded"
					}
					if err := inv.AddFeature(ctx, "harness-id", inventory.Feature{Id: "feature-id", Skipped: skipReason}); err != nil {
						t.Fatal(err)
					}
					store := &ProviderStore{
						inv:       &mmap[string, *inventory.Inventory]{store: map[string]*inventory.Inventory{seed: inv}},
						harnesses: &mmap[string, harness.Harness]{store: make(map[string]harness.Harness)},
					}
					base := &BaseHarnessResource{store: store}
					data := BaseHarnessResourceModel{
						Id: types.StringValue("harness-id"), Name: types.StringValue("harness"),
						Inventory: InventoryDataSourceModel{Seed: types.StringValue(seed)},
						Timeouts:  timeouts.Value{Object: types.ObjectNull(map[string]attr.Type{"create": types.StringType})},
					}
					plan := tfsdk.Plan{Schema: schema.Schema{Attributes: base.schemaAttributes(ctx)}}
					if ds := plan.Set(ctx, data); ds.HasError() {
						t.Fatal(ds)
					}
					var harnessDiags diag.Diagnostics
					harnessCtx, endHarness := traceHarness(ctx, data.Id.ValueString(), data.Name.ValueString(), &harnessDiags)
					harnessDiags = base.do(harnessCtx, framework.CreateOrUpdateRequest{Plan: plan}, host.NewHost())
					endHarness()
					if harnessDiags.HasError() {
						t.Fatal(harnessDiags)
					}

					step := func(phase string) FeatureStepModel {
						cmd := "true"
						if tc.failPhase == phase {
							cmd = "false"
						}
						return FeatureStepModel{Name: types.StringValue(phase), Cmd: types.StringValue(cmd)}
					}
					feature := FeatureResourceModel{
						Id: types.StringValue("feature-id"), Name: types.StringValue("feature"), Timeouts: data.Timeouts,
						Harness: FeatureHarnessResourceModel{Id: data.Id, Name: data.Name, Inventory: data.Inventory},
						Before:  []FeatureStepModel{step("before")}, Steps: []FeatureStepModel{step("assessment")}, After: []FeatureStepModel{step("after")},
						WarnOnFailure: types.BoolValue(tc.warn),
					}
					if tc.retry {
						feature.Steps[0].Retry = &FeatureStepBackoffModel{Attempts: types.Int64Value(2), Delay: types.StringValue("0s"), Factor: types.Float64Value(1)}
					}
					if tc.skipped {
						feature.Skipped = types.StringValue(skipReason)
					}
					ds := (&FeatureResource{store: store}).do(ctx, feature)
					if ds.HasError() != (tc.failPhase != "" && !tc.warn) {
						t.Fatalf("unexpected diagnostics: %v", ds)
					}
					spans := recorder.Ended()
					if len(spans) != len(recorder.Started()) {
						t.Fatal("unclosed spans")
					}
					wantCount := tc.wantSteps + 3
					if tc.skipped {
						wantCount = 1
					}
					if len(spans) != wantCount {
						t.Fatalf("got %d spans, want %d", len(spans), wantCount)
					}

					var featureSpan sdktrace.ReadOnlySpan
					for _, s := range spans {
						if s.Name() == "imagetest.feature" {
							featureSpan = s
						}
					}
					for _, s := range spans {
						if s.SpanContext().TraceID() != trace.SpanContextFromContext(parent).TraceID() {
							t.Errorf("%s has wrong trace", s.Name())
						}
						attrs := attribute.NewSet(s.Attributes()...)
						switch s.Name() {
						case "imagetest.harness", "imagetest.feature":
							if s.Parent().SpanID() != trace.SpanContextFromContext(parent).SpanID() {
								t.Errorf("%s has wrong parent", s.Name())
							}
							wantID := "harness-id"
							if s.Name() == "imagetest.feature" {
								wantID = "feature-id"
							}
							if value, _ := attrs.Value("tf.resource.id"); value.AsString() != wantID {
								t.Errorf("%s has wrong resource ID: %v", s.Name(), value)
							}
						case "imagetest.step", "imagetest.teardown":
							if featureSpan == nil || s.Parent().SpanID() != featureSpan.SpanContext().SpanID() {
								t.Errorf("%s is not a child of the feature", s.Name())
							}
						default:
							t.Errorf("unexpected span %s", s.Name())
						}
						phase, _ := attrs.Value("test.phase")
						wantError := s.Name() == "imagetest.feature" && tc.failPhase != "" || s.Name() == "imagetest.step" && phase.AsString() == tc.failPhase
						if (s.Status().Code == codes.Error) != wantError {
							t.Errorf("%s phase %s has unexpected status: %v", s.Name(), phase.AsString(), s.Status())
						}
					}
				})
			}
		})
	}
}

func TestHarnessConstructionTracing(t *testing.T) {
	for _, kind := range []string{"docker", "k3s"} {
		for _, operation := range []string{"create", "update"} {
			t.Run(kind+"/"+operation, func(t *testing.T) {
				t.Setenv("TRACEPARENT", legacyTraceparent)
				recorder := tracetest.NewSpanRecorder()
				tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
				previous := otel.GetTracerProvider()
				otel.SetTracerProvider(tp)
				t.Cleanup(func() {
					otel.SetTracerProvider(previous)
					if err := tp.Shutdown(context.WithoutCancel(t.Context())); err != nil {
						t.Error(err)
					}
				})
				base := BaseHarnessResourceModel{Id: types.StringValue("harness-id"), Name: types.StringValue("harness"), Inventory: InventoryDataSourceModel{Seed: types.StringValue(t.TempDir())}, Timeouts: timeouts.Value{Object: types.ObjectNull(map[string]attr.Type{"create": types.StringType})}}
				store := new(ProviderStore)
				var r resource.Resource
				var data any
				if kind == "docker" {
					r = &HarnessDockerResource{store: store}
					data = HarnessDockerResourceModel{BaseHarnessResourceModel: base}
				} else {
					r = &HarnessK3sResource{store: store}
					data = HarnessK3sResourceModel{BaseHarnessResourceModel: base}
				}
				schemaResponse := new(resource.SchemaResponse)
				r.Schema(t.Context(), resource.SchemaRequest{}, schemaResponse)
				plan := tfsdk.Plan{Schema: schemaResponse.Schema}
				if ds := plan.Set(t.Context(), data); ds.HasError() {
					t.Fatal(ds)
				}
				// Cancel before construction so the real APKO path fails without fetching packages or starting Docker.
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				var diagnostics diag.Diagnostics
				if operation == "create" {
					response := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema}}
					r.Create(ctx, resource.CreateRequest{Plan: plan}, &response)
					diagnostics = response.Diagnostics
				} else {
					response := resource.UpdateResponse{State: tfsdk.State{Schema: plan.Schema}}
					r.Update(ctx, resource.UpdateRequest{Plan: plan}, &response)
					diagnostics = response.Diagnostics
				}
				if !diagnostics.HasError() {
					t.Fatal("expected canceled construction to fail")
				}
				if len(recorder.Started()) != len(recorder.Ended()) {
					t.Fatal("unclosed construction spans")
				}
				var root sdktrace.ReadOnlySpan
				for _, s := range recorder.Ended() {
					if s.Name() == "imagetest.harness" {
						root = s
					}
				}
				if root == nil {
					t.Fatal("missing harness span on construction failure")
				}
				parent := propagation.TraceContext{}.Extract(t.Context(), propagation.MapCarrier{"traceparent": legacyTraceparent})
				if root.Parent().SpanID() != trace.SpanContextFromContext(parent).SpanID() || root.SpanContext().TraceID() != trace.SpanContextFromContext(parent).TraceID() {
					t.Fatal("harness did not inherit incoming context")
				}
				if root.Status().Code != codes.Error {
					t.Fatalf("harness status: %v", root.Status())
				}
				attrs := attribute.NewSet(root.Attributes()...)
				if id, _ := attrs.Value("tf.resource.id"); id.AsString() != "harness-id" {
					t.Fatalf("resource ID: %v", id)
				}
				var foundBuild bool
				for _, s := range recorder.Ended() {
					if s.InstrumentationScope().Name == "apko" && s.Name() == "New" {
						foundBuild = true
						if s.SpanContext().TraceID() != root.SpanContext().TraceID() {
							t.Errorf("build span %s is in a disconnected trace", s.Name())
						}
						if s.Parent().SpanID() != root.SpanContext().SpanID() {
							t.Errorf("build span %s has parent %s, want harness %s", s.Name(), s.Parent().SpanID(), root.SpanContext().SpanID())
						}
					}
				}
				if !foundBuild {
					t.Fatalf("no APKO spans: %v", recorder.Ended())
				}
			})
		}
	}
}
