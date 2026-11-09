package db

import (
	"context"
	"fmt"
	"reflect"

	"FileEngine/internal/secret"

	"gorm.io/gorm/schema"
)

// secretSerializer transparently encrypts string columns tagged
// `serializer:secret` (Filesystem.Password, PrivateKey, KeyPassphrase, ModelProvider.APIKey) with the
// AES-GCM key from internal/secret. The in-memory struct always holds
// plaintext; only the DB sees ciphertext.
type secretSerializer struct{}

func init() {
	schema.RegisterSerializer("secret", secretSerializer{})
}

func (secretSerializer) Scan(ctx context.Context, field *schema.Field, dst reflect.Value, dbValue any) error {
	var stored string
	switch v := dbValue.(type) {
	case nil:
	case string:
		stored = v
	case []byte:
		stored = string(v)
	default:
		return fmt.Errorf("secret column %s: unexpected type %T", field.Name, dbValue)
	}
	plain, err := secret.Decrypt(stored)
	if err != nil {
		return fmt.Errorf("secret column %s: %w", field.Name, err)
	}
	field.ReflectValueOf(ctx, dst).SetString(plain)
	return nil
}

func (secretSerializer) Value(_ context.Context, field *schema.Field, _ reflect.Value, fieldValue any) (any, error) {
	s, ok := fieldValue.(string)
	if !ok {
		return nil, fmt.Errorf("secret column %s: unexpected type %T", field.Name, fieldValue)
	}
	return secret.Encrypt(s)
}
