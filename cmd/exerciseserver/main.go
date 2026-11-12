// Command exerciseserver serves HTML and PDF exercises to logged-in users.
package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/redjhawk/exerciseserver/internal/store"
	"github.com/redjhawk/exerciseserver/internal/web"
)

// version is set at build time with -ldflags "-X main.version=..."
var version = "dev"

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func usage() {
	fmt.Fprintf(os.Stderr, `exerciseserver %s

Uso:
  exerciseserver [serve]                 arranca el servidor web
  exerciseserver admin-password          crea el usuario "admin" o le pone una contraseña nueva
  exerciseserver import [opciones] RUTA… importa ficheros .html/.pdf (o carpetas)
  exerciseserver version                 muestra la versión

Variables de entorno:
  EXERCISESERVER_DATA_DIR  directorio de datos (por defecto ./data)
  EXERCISESERVER_ADDR      dirección de escucha (por defecto 127.0.0.1:8090)
`, version)
}

func main() {
	log.SetFlags(0)
	cmd := "serve"
	args := os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	dataDir := env("EXERCISESERVER_DATA_DIR", "data")

	var err error
	switch cmd {
	case "serve":
		err = serve(dataDir, env("EXERCISESERVER_ADDR", "127.0.0.1:8090"))
	case "admin-password":
		err = adminPassword(dataDir)
	case "import":
		err = importCmd(dataDir, args)
	case "version", "--version", "-v":
		fmt.Println(version)
	case "help", "-h", "--help":
		usage()
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		log.Fatalf("error: %v", err)
	}
}

func openStore(dataDir string) (*store.Store, error) {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, err
	}
	return store.Open(filepath.Join(dataDir, "exerciseserver.sqlite"))
}

func serve(dataDir, addr string) error {
	log.SetFlags(log.LstdFlags)
	st, err := openStore(dataDir)
	if err != nil {
		return err
	}
	defer st.Close()
	srv, err := web.New(st, dataDir, version)
	if err != nil {
		return err
	}
	if n, _ := st.CountActiveAdmins(); n == 0 {
		log.Printf("AVISO: no hay ningún administrador. Ejecuta «exerciseserver admin-password» para crearlo.")
	}
	hs := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		hs.Shutdown(sctx)
	}()
	log.Printf("exerciseserver %s escuchando en http://%s (datos en %s)", version, addr, dataDir)
	if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func randomPassword(n int) string {
	const alphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, n)
	for i := range b {
		x, _ := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		b[i] = alphabet[x.Int64()]
	}
	return string(b)
}

func adminPassword(dataDir string) error {
	st, err := openStore(dataDir)
	if err != nil {
		return err
	}
	defer st.Close()
	pw := randomPassword(16)
	if err := st.SetAdminPassword(pw); err != nil {
		return err
	}
	fmt.Printf("Username: admin\nPassword: %s\n", pw)
	return nil
}

func importCmd(dataDir string, args []string) error {
	fset := flag.NewFlagSet("import", flag.ExitOnError)
	level := fset.String("level", "", "nivel para todos los ficheros (se crea si no existe)")
	tags := fset.String("tags", "", "etiquetas separadas por comas para todos los ficheros")
	folderTags := fset.Bool("folder-tags", true, "añadir como etiquetas los nombres de las subcarpetas")
	fset.Usage = func() {
		fmt.Fprintln(os.Stderr, "Uso: exerciseserver import [-level NIVEL] [-tags a,b] [-folder-tags=false] RUTA…")
		fset.PrintDefaults()
	}
	fset.Parse(args)
	if fset.NArg() == 0 {
		fset.Usage()
		return errors.New("falta al menos una ruta")
	}
	st, err := openStore(dataDir)
	if err != nil {
		return err
	}
	defer st.Close()
	srv, err := web.New(st, dataDir, version)
	if err != nil {
		return err
	}
	var levelID int64
	if *level != "" {
		if levelID, err = st.LevelByName(*level); err != nil {
			return err
		}
	}
	baseTags := store.ParseTags(*tags)
	imported, skipped := 0, 0

	importOne := func(path string, extraTags []string) error {
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".html" && ext != ".htm" && ext != ".pdf" {
			return nil
		}
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if exists, err := st.FileExists(filepath.Base(path), info.Size()); err != nil {
			return err
		} else if exists {
			fmt.Printf("  ya existe, se omite: %s\n", path)
			skipped++
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		t := store.ParseTags(strings.Join(append(append([]string{}, baseTags...), extraTags...), ","))
		if _, err := srv.SaveFile(filepath.Base(path), f, levelID, t, ""); err != nil {
			return err
		}
		fmt.Printf("  importado: %s %v\n", path, t)
		imported++
		return nil
	}

	for _, root := range fset.Args() {
		info, err := os.Stat(root)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			if err := importOne(root, nil); err != nil {
				return err
			}
			continue
		}
		err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if path != root && strings.HasPrefix(d.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			}
			var extra []string
			if *folderTags {
				rel, _ := filepath.Rel(root, filepath.Dir(path))
				if rel != "." {
					extra = strings.Split(filepath.ToSlash(rel), "/")
				}
			}
			return importOne(path, extra)
		})
		if err != nil {
			return err
		}
	}
	fmt.Printf("Importados: %d. Omitidos (ya existían): %d.\n", imported, skipped)
	return nil
}
