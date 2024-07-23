package features

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"k8s.io/apimachinery/pkg/util/wait"
)

func TestFeature(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name        string
		befores     func(*bytes.Buffer) []*step
		assessments func(*bytes.Buffer) []*step
		afters      func(*bytes.Buffer) []*step
		wantout     string
		wanterr     string
	}{
		{
			name: "Success",
			befores: func(b *bytes.Buffer) []*step {
				return []*step{
					{Name: "Before Step", Fn: func(ctx context.Context) error {
						b.WriteString("before ")
						return nil
					}},
				}
			},
			assessments: func(b *bytes.Buffer) []*step {
				return []*step{
					{Name: "Assessment Step", Fn: func(ctx context.Context) error {
						b.WriteString("asssessment ")
						return nil
					}},
				}
			},
			afters: func(b *bytes.Buffer) []*step {
				return []*step{
					{Name: "After Step", Fn: func(ctx context.Context) error {
						b.WriteString("after ")
						return nil
					}},
				}
			},
			wantout: "before asssessment after ",
			wanterr: "",
		},
		{
			name: "ShortCircuitBeforeFailure",
			befores: func(b *bytes.Buffer) []*step {
				return []*step{
					{Name: "Before Step", Fn: func(ctx context.Context) error { return errors.New("before step error") }},
				}
			},
			assessments: func(b *bytes.Buffer) []*step {
				return []*step{}
			},
			afters: func(b *bytes.Buffer) []*step {
				return []*step{
					{Name: "After Step", Fn: func(ctx context.Context) error {
						b.WriteString("after ")
						return nil
					}},
				}
			},
			wantout: "after ",
			wanterr: "before step error",
		},
		{
			name: "ShortCircuitAssessmentFailure",
			befores: func(b *bytes.Buffer) []*step {
				return []*step{}
			},
			assessments: func(b *bytes.Buffer) []*step {
				return []*step{
					{Name: "Assessment Step", Fn: func(ctx context.Context) error {
						b.WriteString("assessment ")
						return errors.New("assessment step error")
					}},
				}
			},
			afters: func(b *bytes.Buffer) []*step {
				return []*step{
					{Name: "After Step", Fn: func(ctx context.Context) error {
						b.WriteString("after ")
						return nil
					}},
					{Name: "After Step 2", Fn: func(ctx context.Context) error {
						return errors.New("after step error")
					}},
				}
			},
			wantout: "assessment after ",
			wanterr: "assessment step error; after step error",
		},
		{
			name: "Retry",
			befores: func(b *bytes.Buffer) []*step {
				return []*step{}
			},
			assessments: func(b *bytes.Buffer) []*step {
				return []*step{
					tstepWithRetry(&step{Name: "Assessment Step", Fn: func(ctx context.Context) error {
						b.WriteString("foo ")
						return errors.New("assessment step error")
					}}, wait.Backoff{
						Steps:    3,
						Duration: 100 * time.Millisecond,
						Factor:   1.0,
					}),
				}
			},
			afters: func(b *bytes.Buffer) []*step {
				return []*step{}
			},
			wantout: "foo foo foo ",
			// This will always fail, so just grep on the error message
			wanterr: "timed out waiting for the condition",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := New(tt.name)

			var buf bytes.Buffer

			befores := tt.befores(&buf)
			for _, s := range befores {
				f.WithBefore(s.Name, s.Fn)
			}

			assessments := tt.assessments(&buf)
			for _, s := range assessments {
				f.WithAssessment(s.Name, s.Fn)
			}

			afters := tt.afters(&buf)
			for _, s := range afters {
				f.WithAfter(s.Name, s.Fn)
			}

			err := f.Test(ctx)

			if diff := cmp.Diff(tt.wantout, buf.String()); diff != "" {
				t.Errorf("unexpected output (-want +got):\n%s", diff)
			}

			if (err != nil && err.Error() != tt.wanterr) || (err == nil && tt.wanterr != "") {
				t.Errorf("expected error: `%v`, got: `%v`", tt.wanterr, err)
			}
		})
	}
}

func tstepWithRetry(s *step, backoff wait.Backoff) *step {
	StepWithRetry(backoff)(s)
	return s
}
