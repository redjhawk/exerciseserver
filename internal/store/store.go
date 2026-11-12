// Package store keeps users, sessions, levels, tags and exercise files in SQLite.
package store

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")

type Store struct {
	db *sql.DB
}

type User struct {
	ID        int64
	Username  string
	IsAdmin   bool
	Active    bool
	CreatedAt time.Time
}

type Session struct {
	User      User
	CSRFToken string
}

type Level struct {
	ID       int64
	Name     string
	Position int
	Count    int
}

type Tag struct {
	ID    int64
	Name  string
	Count int
}

type File struct {
	ID          int64
	Title       string
	Description string
	Kind        string // "html" or "pdf"
	OrigName    string
	StoredName  string
	Size        int64
	LevelID     sql.NullInt64
	LevelName   string
	LevelPos    int
	Tags        []string
	UploadedAt  time.Time
}

const schema = `
CREATE TABLE IF NOT EXISTS users (
	id INTEGER PRIMARY KEY,
	username TEXT NOT NULL UNIQUE COLLATE NOCASE,
	password_hash TEXT NOT NULL,
	is_admin INTEGER NOT NULL DEFAULT 0,
	active INTEGER NOT NULL DEFAULT 1,
	created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
	token_hash TEXT PRIMARY KEY,
	user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	csrf_token TEXT NOT NULL,
	expires_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS levels (
	id INTEGER PRIMARY KEY,
	name TEXT NOT NULL UNIQUE COLLATE NOCASE,
	position INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS tags (
	id INTEGER PRIMARY KEY,
	name TEXT NOT NULL UNIQUE COLLATE NOCASE
);
CREATE TABLE IF NOT EXISTS files (
	id INTEGER PRIMARY KEY,
	title TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	kind TEXT NOT NULL CHECK (kind IN ('html','pdf')),
	orig_name TEXT NOT NULL,
	stored_name TEXT NOT NULL UNIQUE,
	size INTEGER NOT NULL,
	level_id INTEGER REFERENCES levels(id) ON DELETE SET NULL,
	uploaded_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS file_tags (
	file_id INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
	tag_id INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
	PRIMARY KEY (file_id, tag_id)
);
`

func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// ---------- users ----------

var dummyHash = sync.OnceValue(func() []byte {
	h, _ := bcrypt.GenerateFromPassword([]byte("dummy-password"), bcrypt.DefaultCost)
	return h
})

func hashPassword(pw string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(h), err
}

func (s *Store) CreateUser(username, password string, admin bool) error {
	username = strings.TrimSpace(username)
	if username == "" || len(password) < 4 {
		return errors.New("usuario vacío o contraseña demasiado corta (mínimo 4 caracteres)")
	}
	h, err := hashPassword(password)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO users(username,password_hash,is_admin,active,created_at) VALUES(?,?,?,1,?)`,
		username, h, admin, time.Now().Unix())
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return errors.New("ese usuario ya existe")
	}
	return err
}

// SetAdminPassword creates or resets the "admin" account and ends its sessions.
func (s *Store) SetAdminPassword(password string) error {
	h, err := hashPassword(password)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO users(username,password_hash,is_admin,active,created_at) VALUES('admin',?,1,1,?)
		ON CONFLICT(username) DO UPDATE SET password_hash=excluded.password_hash, is_admin=1, active=1`, h, time.Now().Unix()); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM sessions WHERE user_id=(SELECT id FROM users WHERE username='admin')`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Authenticate(username, password string) (User, bool) {
	var u User
	var hash string
	var created int64
	err := s.db.QueryRow(`SELECT id,username,password_hash,is_admin,active,created_at FROM users WHERE username=?`,
		strings.TrimSpace(username)).Scan(&u.ID, &u.Username, &hash, &u.IsAdmin, &u.Active, &created)
	if err != nil {
		// spend similar time to avoid user enumeration by timing
		bcrypt.CompareHashAndPassword(dummyHash(), []byte(password))
		return User{}, false
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil || !u.Active {
		return User{}, false
	}
	u.CreatedAt = time.Unix(created, 0)
	return u, true
}

func (s *Store) ListUsers() ([]User, error) {
	rows, err := s.db.Query(`SELECT id,username,is_admin,active,created_at FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		var c int64
		if err := rows.Scan(&u.ID, &u.Username, &u.IsAdmin, &u.Active, &c); err != nil {
			return nil, err
		}
		u.CreatedAt = time.Unix(c, 0)
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) SetUserPassword(id int64, password string) error {
	if len(password) < 4 {
		return errors.New("contraseña demasiado corta (mínimo 4 caracteres)")
	}
	h, err := hashPassword(password)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(`UPDATE users SET password_hash=? WHERE id=?`, h, id); err != nil {
		return err
	}
	_, err = s.db.Exec(`DELETE FROM sessions WHERE user_id=?`, id)
	return err
}

func (s *Store) SetUserFlags(id int64, active, admin bool) error {
	if _, err := s.db.Exec(`UPDATE users SET active=?, is_admin=? WHERE id=?`, active, admin, id); err != nil {
		return err
	}
	if !active {
		_, err := s.db.Exec(`DELETE FROM sessions WHERE user_id=?`, id)
		return err
	}
	return nil
}

func (s *Store) DeleteUser(id int64) error {
	_, err := s.db.Exec(`DELETE FROM users WHERE id=?`, id)
	return err
}

func (s *Store) CountActiveAdmins() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM users WHERE is_admin=1 AND active=1`).Scan(&n)
	return n, err
}

func (s *Store) GetUser(id int64) (User, error) {
	var u User
	var c int64
	err := s.db.QueryRow(`SELECT id,username,is_admin,active,created_at FROM users WHERE id=?`, id).
		Scan(&u.ID, &u.Username, &u.IsAdmin, &u.Active, &c)
	if errors.Is(err, sql.ErrNoRows) {
		return u, ErrNotFound
	}
	u.CreatedAt = time.Unix(c, 0)
	return u, err
}

// ---------- sessions ----------

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func tokenHash(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

// CreateSession returns the raw token to put in the cookie.
func (s *Store) CreateSession(userID int64, ttl time.Duration) (string, error) {
	tok := randomToken()
	_, err := s.db.Exec(`INSERT INTO sessions(token_hash,user_id,csrf_token,expires_at) VALUES(?,?,?,?)`,
		tokenHash(tok), userID, randomToken(), time.Now().Add(ttl).Unix())
	s.db.Exec(`DELETE FROM sessions WHERE expires_at < ?`, time.Now().Unix())
	return tok, err
}

func (s *Store) GetSession(token string) (Session, bool) {
	var ss Session
	var c int64
	err := s.db.QueryRow(`SELECT u.id,u.username,u.is_admin,u.active,u.created_at,s.csrf_token
		FROM sessions s JOIN users u ON u.id=s.user_id
		WHERE s.token_hash=? AND s.expires_at>? AND u.active=1`, tokenHash(token), time.Now().Unix()).
		Scan(&ss.User.ID, &ss.User.Username, &ss.User.IsAdmin, &ss.User.Active, &c, &ss.CSRFToken)
	if err != nil {
		return Session{}, false
	}
	ss.User.CreatedAt = time.Unix(c, 0)
	return ss, true
}

func (s *Store) DeleteSession(token string) {
	s.db.Exec(`DELETE FROM sessions WHERE token_hash=?`, tokenHash(token))
}

// ---------- levels ----------

func (s *Store) ListLevels() ([]Level, error) {
	rows, err := s.db.Query(`SELECT l.id,l.name,l.position,(SELECT COUNT(*) FROM files f WHERE f.level_id=l.id)
		FROM levels l ORDER BY l.position, l.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Level
	for rows.Next() {
		var l Level
		if err := rows.Scan(&l.ID, &l.Name, &l.Position, &l.Count); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) CreateLevel(name string, pos int) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("el nombre del nivel está vacío")
	}
	_, err := s.db.Exec(`INSERT INTO levels(name,position) VALUES(?,?)`, name, pos)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return errors.New("ese nivel ya existe")
	}
	return err
}

func (s *Store) UpdateLevel(id int64, name string, pos int) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("el nombre del nivel está vacío")
	}
	_, err := s.db.Exec(`UPDATE levels SET name=?, position=? WHERE id=?`, name, pos, id)
	return err
}

func (s *Store) DeleteLevel(id int64) error {
	_, err := s.db.Exec(`DELETE FROM levels WHERE id=?`, id)
	return err
}

// LevelByName finds or creates a level.
func (s *Store) LevelByName(name string) (int64, error) {
	name = strings.TrimSpace(name)
	var id int64
	err := s.db.QueryRow(`SELECT id FROM levels WHERE name=?`, name).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	var maxPos int
	s.db.QueryRow(`SELECT COALESCE(MAX(position),0) FROM levels`).Scan(&maxPos)
	res, err := s.db.Exec(`INSERT INTO levels(name,position) VALUES(?,?)`, name, maxPos+10)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ---------- tags ----------

func (s *Store) ListTags() ([]Tag, error) {
	rows, err := s.db.Query(`SELECT t.id,t.name,(SELECT COUNT(*) FROM file_tags ft WHERE ft.tag_id=t.id)
		FROM tags t ORDER BY t.name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Tag
	for rows.Next() {
		var t Tag
		if err := rows.Scan(&t.ID, &t.Name, &t.Count); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) RenameTag(id int64, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("el nombre de la etiqueta está vacío")
	}
	var other int64
	err := s.db.QueryRow(`SELECT id FROM tags WHERE name=? AND id<>?`, name, id).Scan(&other)
	if err == nil {
		// merge into the existing tag
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err := tx.Exec(`INSERT OR IGNORE INTO file_tags(file_id,tag_id) SELECT file_id,? FROM file_tags WHERE tag_id=?`, other, id); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM tags WHERE id=?`, id); err != nil {
			return err
		}
		return tx.Commit()
	}
	_, err = s.db.Exec(`UPDATE tags SET name=? WHERE id=?`, name, id)
	return err
}

func (s *Store) DeleteTag(id int64) error {
	_, err := s.db.Exec(`DELETE FROM tags WHERE id=?`, id)
	return err
}

// ParseTags splits a comma separated list, trimming and de-duplicating (case-insensitive).
func ParseTags(raw string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range strings.Split(raw, ",") {
		p = strings.Join(strings.Fields(strings.ReplaceAll(p, "|", "/")), " ")
		if p == "" || seen[strings.ToLower(p)] {
			continue
		}
		seen[strings.ToLower(p)] = true
		out = append(out, p)
	}
	return out
}

func setFileTags(tx *sql.Tx, fileID int64, tags []string) error {
	if _, err := tx.Exec(`DELETE FROM file_tags WHERE file_id=?`, fileID); err != nil {
		return err
	}
	for _, t := range tags {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO tags(name) VALUES(?)`, t); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO file_tags(file_id,tag_id) SELECT ?,id FROM tags WHERE name=?`, fileID, t); err != nil {
			return err
		}
	}
	return nil
}

// ---------- files ----------

type NewFile struct {
	Title       string
	Description string
	Kind        string
	OrigName    string
	StoredName  string
	Size        int64
	LevelID     int64 // 0 = none
	Tags        []string
}

func nullID(id int64) sql.NullInt64 {
	return sql.NullInt64{Int64: id, Valid: id > 0}
}

func (s *Store) CreateFile(f NewFile) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT INTO files(title,description,kind,orig_name,stored_name,size,level_id,uploaded_at) VALUES(?,?,?,?,?,?,?,?)`,
		f.Title, f.Description, f.Kind, f.OrigName, f.StoredName, f.Size, nullID(f.LevelID), time.Now().Unix())
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	if err := setFileTags(tx, id, f.Tags); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

func (s *Store) UpdateFile(id int64, title, description string, levelID int64, tags []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE files SET title=?, description=?, level_id=? WHERE id=?`,
		title, description, nullID(levelID), id); err != nil {
		return err
	}
	if err := setFileTags(tx, id, tags); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM tags WHERE id NOT IN (SELECT tag_id FROM file_tags)`); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteFile removes the row and returns the stored name so the caller can delete it from disk.
func (s *Store) DeleteFile(id int64) (string, error) {
	f, err := s.GetFile(id)
	if err != nil {
		return "", err
	}
	if _, err := s.db.Exec(`DELETE FROM files WHERE id=?`, id); err != nil {
		return "", err
	}
	s.db.Exec(`DELETE FROM tags WHERE id NOT IN (SELECT tag_id FROM file_tags)`)
	return f.StoredName, nil
}

const fileCols = `f.id,f.title,f.description,f.kind,f.orig_name,f.stored_name,f.size,f.level_id,
	COALESCE(l.name,''),COALESCE(l.position,1000000),f.uploaded_at,
	COALESCE((SELECT group_concat(t.name, char(31)) FROM file_tags ft JOIN tags t ON t.id=ft.tag_id WHERE ft.file_id=f.id),'')`

func scanFile(sc interface{ Scan(...any) error }) (File, error) {
	var f File
	var up int64
	var tags string
	err := sc.Scan(&f.ID, &f.Title, &f.Description, &f.Kind, &f.OrigName, &f.StoredName, &f.Size, &f.LevelID,
		&f.LevelName, &f.LevelPos, &up, &tags)
	if err != nil {
		return f, err
	}
	f.UploadedAt = time.Unix(up, 0)
	if tags != "" {
		f.Tags = strings.Split(tags, "\x1f")
		sort.Slice(f.Tags, func(i, j int) bool { return strings.ToLower(f.Tags[i]) < strings.ToLower(f.Tags[j]) })
	}
	return f, nil
}

func (s *Store) GetFile(id int64) (File, error) {
	f, err := scanFile(s.db.QueryRow(`SELECT `+fileCols+` FROM files f LEFT JOIN levels l ON l.id=f.level_id WHERE f.id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return f, ErrNotFound
	}
	return f, err
}

// FileExists reports whether a file with the same original name and size was already added.
func (s *Store) FileExists(origName string, size int64) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM files WHERE orig_name=? AND size=?`, origName, size).Scan(&n)
	return n > 0, err
}

func (s *Store) ListFiles() ([]File, error) {
	rows, err := s.db.Query(`SELECT ` + fileCols + ` FROM files f LEFT JOIN levels l ON l.id=f.level_id
		ORDER BY COALESCE(l.position,1000000), f.title COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []File
	for rows.Next() {
		f, err := scanFile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
