package ec2

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStack(t *testing.T) {
	t.Run("ensure-LIFO-order", func(t *testing.T) {
		bits := uint8(0b11111111)
		stack := new(Stack)
		stack.Push(func(ctx context.Context) error {
			bits &^= 1 << 5
			require.Equal(t, uint8(0b00011111), bits, "%08b", bits)
			return nil
		})
		stack.Push(func(ctx context.Context) error {
			bits &^= 1 << 6
			require.Equal(t, uint8(0b00111111), bits)
			return nil
		})
		stack.Push(func(ctx context.Context) error {
			bits &^= 1 << 7
			require.Equal(t, uint8(0b01111111), bits)
			return nil
		})
		require.NoError(t, stack.Destroy(t.Context()))
	})
	t.Run("ensure-errors-joined", func(t *testing.T) {
		err1 := fmt.Errorf("one")
		err2 := fmt.Errorf("two")
		stack := new(Stack)
		stack.Push(func(ctx context.Context) error {
			return err1
		})
		stack.Push(func(ctx context.Context) error {
			return err2
		})
		// And a nil-return just for good measure.
		stack.Push(func(ctx context.Context) error {
			return nil
		})
		err := stack.Destroy(t.Context())
		require.ErrorIs(t, err, err1)
		require.ErrorIs(t, err, err2)
	})
}
