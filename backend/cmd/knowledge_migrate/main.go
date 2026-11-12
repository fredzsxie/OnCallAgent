package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/internal/service/knowledge"
	"github.com/fredzsxie/OnCallAgent/backend/utility/client"
	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "knowledge migration failed:", err)
		os.Exit(1)
	}
}
func run() error {
	source := flag.String("source-collection", "oncall_knowledge", "existing collection to read")
	target := flag.String("target-collection", "oncall_knowledge_v2", "v2 collection to create or resume")
	apply := flag.Bool("apply", false, "apply the inspected migration; knowledge writers must be stopped")
	dry := flag.Bool("dry-run", false, "inspect only (the default)")
	versions := flag.String("versions", "", "JSON file mapping logical sources to explicitly selected legacy versions")
	timeout := flag.Duration("timeout", 30*time.Minute, "total migration budget")
	flag.Parse()
	if flag.NArg() != 0 || *source == "" || *target == "" || *source == *target || (*apply && *dry) || *timeout <= 0 {
		return errors.New("invalid migration arguments")
	}
	if err := config.LoadEnv(); err != nil {
		return err
	}
	kc, err := config.LoadKnowledge()
	if err != nil {
		return err
	}
	mc, err := config.LoadMilvus()
	if err != nil {
		return err
	}
	ec, err := config.LoadEmbedding()
	if err != nil {
		return err
	}
	choices := map[string]string{}
	if *versions != "" {
		raw, err := os.ReadFile(*versions)
		if err != nil {
			return err
		}
		if err = json.Unmarshal(raw, &choices); err != nil {
			return err
		}
	}
	root, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(root, *timeout)
	defer cancel()
	ctx, logs, err := logger.Bootstrap(ctx, false)
	if err != nil {
		return err
	}
	defer logs.Close()
	db, err := client.NewMilvus(ctx, mc)
	if err != nil {
		return err
	}
	defer db.Close()
	existing, err := db.DescribeCollection(ctx, *source)
	if err != nil {
		return err
	}
	if diff := knowledge.SchemaDiff(existing.Schema, knowledge.VectorDimension(existing.Schema)); len(diff) > 0 {
		return errors.New("source must have the legacy knowledge schema")
	}
	plan, err := knowledge.InspectLegacy(ctx, db, *source, kc.FileDir, choices)
	if err != nil {
		return err
	}
	report := struct {
		DryRun bool   `json:"dryRun"`
		Source string `json:"sourceCollection"`
		Target string `json:"targetCollection"`
		knowledge.MigrationPlan
		Completed int  `json:"completed"`
		Verified  bool `json:"verified"`
	}{DryRun: !*apply, Source: *source, Target: *target, MigrationPlan: plan}
	if !*apply {
		return json.NewEncoder(os.Stdout).Encode(report)
	}
	if plan.Conflicts > 0 {
		_ = json.NewEncoder(os.Stdout).Encode(report)
		return knowledge.ErrConflict
	}
	mc.Collection = *target
	emb, err := client.NewEmbedding(ctx, ec)
	if err != nil {
		return err
	}
	defer emb.Close()
	if _, err = knowledge.InitV2(ctx, db, emb, mc); err != nil {
		return err
	}
	store, err := knowledge.NewStore(ctx, db, emb, mc)
	if err != nil {
		return err
	}
	catalog, err := knowledge.OpenCatalog(kc.CatalogPath)
	if err != nil {
		return err
	}
	defer catalog.Close()
	manager, err := knowledge.NewManager(ctx, kc, catalog, store, store.Profile(ec.Model))
	if err != nil {
		return err
	}
	report.Completed, err = knowledge.ApplyMigration(ctx, manager, plan)
	if err == nil {
		var after knowledge.MigrationPlan
		after, err = knowledge.InspectLegacy(ctx, db, *source, kc.FileDir, choices)
		if err == nil && after.SnapshotHash != plan.SnapshotHash {
			err = errors.New("source changed during migration; keep writes disabled and rerun")
		}
		report.Verified = err == nil
	}
	if encodeErr := json.NewEncoder(os.Stdout).Encode(report); encodeErr != nil {
		return encodeErr
	}
	return err
}
