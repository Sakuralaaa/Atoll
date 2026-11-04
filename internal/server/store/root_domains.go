package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// RootDomainRow is a root domain with its (decrypted) Origin CA certificate.
type RootDomainRow struct {
	api.RootDomain
	CertID  string
	CertPEM string
	KeyPEM  string
}

const rootDomainCols = `name, zone_id, zone_name, cert_mode, cert_status, cert_error, cert_id, cert_expires_at, origin_cert_pem, origin_key_pem, is_default, created_at`

func (s *Store) scanRootDomain(row interface{ Scan(...any) error }) (*RootDomainRow, error) {
	var r RootDomainRow
	var exp, cert, key, created string
	var def int
	if err := row.Scan(&r.Name, &r.ZoneID, &r.ZoneName, &r.CertMode, &r.CertStatus, &r.CertError, &r.CertID, &exp, &cert, &key, &def, &created); err != nil {
		return nil, mapErr(err)
	}
	r.Default = def == 1
	r.CreatedAt = parseTS(created)
	if exp != "" {
		t := parseTS(exp)
		r.CertExpiresAt = &t
	}
	var err error
	if cert != "" {
		if r.CertPEM, err = s.Box.Decrypt(cert); err != nil {
			return nil, err
		}
	}
	if key != "" {
		if r.KeyPEM, err = s.Box.Decrypt(key); err != nil {
			return nil, err
		}
	}
	return &r, nil
}

// ListRootDomains returns every root domain, the default first, then by name.
func (s *Store) ListRootDomains(ctx context.Context) ([]RootDomainRow, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT `+rootDomainCols+` FROM root_domains ORDER BY is_default DESC, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RootDomainRow{}
	for rows.Next() {
		r, err := s.scanRootDomain(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// GetRootDomain returns one root domain.
func (s *Store) GetRootDomain(ctx context.Context, name string) (*RootDomainRow, error) {
	return s.scanRootDomain(s.DB.QueryRowContext(ctx, `SELECT `+rootDomainCols+` FROM root_domains WHERE name=?`, name))
}

// CreateRootDomain inserts a root domain (ErrConflict if it exists). When it is the default
// (or the first one) it becomes the only default and settings.base_domain mirrors it.
func (s *Store) CreateRootDomain(ctx context.Context, r api.RootDomain) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM root_domains`).Scan(&n); err != nil {
		return err
	}
	def := r.Default || n == 0
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now()
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO root_domains(name, zone_id, zone_name, cert_mode, cert_status, is_default, created_at) VALUES(?,?,?,?,?,?,?)`,
		r.Name, r.ZoneID, r.ZoneName, r.CertMode, r.CertStatus, 0, TS(r.CreatedAt)); err != nil {
		return mapErr(err)
	}
	if def {
		if err := setDefaultRootTx(ctx, tx, r.Name); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func setDefaultRootTx(ctx context.Context, tx *sql.Tx, name string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE root_domains SET is_default = CASE WHEN name=? THEN 1 ELSE 0 END`, name); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES('base_domain', ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, name)
	return err
}

// SetDefaultRootDomain makes name the default root domain and mirrors it to settings.base_domain.
func (s *Store) SetDefaultRootDomain(ctx context.Context, name string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var x string
	if err := tx.QueryRowContext(ctx, `SELECT name FROM root_domains WHERE name=?`, name).Scan(&x); err != nil {
		return mapErr(err)
	}
	if err := setDefaultRootTx(ctx, tx, name); err != nil {
		return err
	}
	return tx.Commit()
}

// RenameRootDomain renames a root domain in place (the legacy single base_domain edit),
// keeping its settings; project references follow. Mirrors settings.base_domain when it is
// the default.
func (s *Store) RenameRootDomain(ctx context.Context, from, to string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var def int
	if err := tx.QueryRowContext(ctx, `SELECT is_default FROM root_domains WHERE name=?`, from).Scan(&def); err != nil {
		return mapErr(err)
	}
	// The zone and certificate belonged to the old name: rediscover / reissue.
	if _, err := tx.ExecContext(ctx, `UPDATE root_domains SET name=?, zone_name='', cert_id='', cert_expires_at='', origin_cert_pem='', origin_key_pem='',
		cert_status=CASE WHEN cert_mode='global' THEN 'global' ELSE '' END, cert_error='' WHERE name=?`, to, from); err != nil {
		return mapErr(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE projects SET root_domain=? WHERE root_domain=?`, to, from); err != nil {
		return err
	}
	if def == 1 {
		if err := setDefaultRootTx(ctx, tx, to); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetRootDomainZone records the Cloudflare zone a root domain belongs to.
func (s *Store) SetRootDomainZone(ctx context.Context, name, zoneID, zoneName string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE root_domains SET zone_id=?, zone_name=? WHERE name=?`, zoneID, zoneName, name)
	return err
}

// SetRootDomainCertMode changes the certificate mode; the status is recomputed by the caller.
func (s *Store) SetRootDomainCertMode(ctx context.Context, name, mode string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE root_domains SET cert_mode=? WHERE name=?`, mode, name)
	return err
}

// SetRootDomainCert records the certificate outcome. Empty certPEM/keyPEM keep the stored
// certificate unless clear is set.
func (s *Store) SetRootDomainCert(ctx context.Context, name, status, errMsg, certID string, expires *time.Time, certPEM, keyPEM string, clear bool) error {
	exp := ""
	if expires != nil {
		exp = TS(*expires)
	}
	encCert, encKey := "", ""
	if certPEM != "" && keyPEM != "" {
		var err error
		if encCert, err = s.Box.Encrypt(certPEM); err != nil {
			return err
		}
		if encKey, err = s.Box.Encrypt(keyPEM); err != nil {
			return err
		}
	}
	if encCert != "" || clear {
		_, err := s.DB.ExecContext(ctx, `UPDATE root_domains SET cert_status=?, cert_error=?, cert_id=?, cert_expires_at=?, origin_cert_pem=?, origin_key_pem=? WHERE name=?`,
			status, errMsg, certID, exp, encCert, encKey, name)
		return err
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE root_domains SET cert_status=?, cert_error=? WHERE name=?`, status, errMsg, name)
	return err
}

// DeleteRootDomain removes a root domain and clears project references to it. When it was
// the default, settings.base_domain is cleared too (callers only allow that for the last one).
func (s *Store) DeleteRootDomain(ctx context.Context, name string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var def int
	if err := tx.QueryRowContext(ctx, `SELECT is_default FROM root_domains WHERE name=?`, name).Scan(&def); err != nil {
		return mapErr(err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM root_domains WHERE name=?`, name); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE projects SET root_domain='' WHERE root_domain=?`, name); err != nil {
		return err
	}
	if def == 1 {
		var next string
		err := tx.QueryRowContext(ctx, `SELECT name FROM root_domains ORDER BY created_at, name LIMIT 1`).Scan(&next)
		switch {
		case err == sql.ErrNoRows:
			if _, err := tx.ExecContext(ctx, `UPDATE settings SET value='' WHERE key='base_domain'`); err != nil {
				return err
			}
		case err != nil:
			return err
		default:
			if err := setDefaultRootTx(ctx, tx, next); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// CountDomainsUnder counts bound domains equal to root or under it.
func (s *Store) CountDomainsUnder(ctx context.Context, root string) (int, error) {
	var n int
	err := s.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM domains WHERE hostname=? OR hostname LIKE ?`, root, "%."+root).Scan(&n)
	return n, err
}
