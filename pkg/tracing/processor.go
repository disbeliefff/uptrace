package tracing

import (
	"context"
	"runtime"
	"time"

	"github.com/cespare/xxhash/v2"
	"github.com/uptrace/opentelemetry-go-extra/otelzap"
	"github.com/uptrace/uptrace/pkg/bunapp"
	"github.com/uptrace/uptrace/pkg/bunotel"
	"github.com/uptrace/uptrace/pkg/org"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
	"go4.org/syncutil"
	"golang.org/x/exp/slices"
)

type Processor[T any] struct {
	App       *bunapp.App
	batchSize int
	queue     chan *T
	gate      *syncutil.Gate
	logger    *otelzap.Logger
}

func NewProcessor[T any](app *bunapp.App, batchSize, bufferSize int) *Processor[T] {
	maxprocs := runtime.GOMAXPROCS(0)

	p := &Processor[T]{
		App:       app,
		batchSize: batchSize,
		queue:     make(chan *T, bufferSize),
		gate:      syncutil.NewGate(maxprocs),
		logger:    app.Logger,
	}

	p.logger.Info("starting processor...",
		zap.Int("threads", maxprocs),
		zap.Int("batch_size", batchSize),
		zap.Int("buffer_size", bufferSize))

	app.WaitGroup().Add(1)
	go func() {
		defer app.WaitGroup().Done()
		p.processLoop(app.Context())
	}()

	queueLen, _ := bunotel.Meter.Int64ObservableGauge("uptrace.processor.queue_length",
		metric.WithUnit("{items}"),
	)

	if _, err := bunotel.Meter.RegisterCallback(
		func(ctx context.Context, o metric.Observer) error {
			o.ObserveInt64(queueLen, int64(len(p.queue)))
			return nil
		},
		queueLen,
	); err != nil {
		panic(err)
	}

	return p
}

func (p *Processor[T]) AddItem(ctx context.Context, item *T) {
	p.logger.Info("AddItem called", zap.Any("item", item))
	select {
	case p.queue <- item:
	default:
		p.logger.Error("queue is full (consider increasing buffer size)",
			zap.Int("len", len(p.queue)))
	}
}

func (p *Processor[T]) processLoop(ctx context.Context) {
	p.logger.Info("processLoop started")
	const timeout = 5 * time.Second

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	items := make([]*T, 0, p.batchSize)

loop:
	for {
		p.logger.Info("Waiting for items in the queue")
		select {
		case item := <-p.queue:
			p.logger.Info("Received item from queue", zap.Int("currentBatchSize", len(items)+1), zap.Int("queueLength", len(p.queue)))
			items = append(items, item)

			p.logger.Info("Current batch size after adding item", zap.Int("currentBatchSize", len(items)))

			if len(items) < p.batchSize {
				p.logger.Info("Batch size not reached yet", zap.Int("currentBatchSize", len(items)), zap.Int("requiredBatchSize", p.batchSize))
				break
			}

			p.logger.Info("Processing batch of items", zap.Int("batchSize", len(items)))
			p.processItems(ctx, items)
			items = items[:0]

			if !timer.Stop() {
				<-timer.C
			}
			timer.Reset(timeout)

		case <-timer.C:
			if len(items) > 0 {
				p.logger.Info("Processing batch due to timeout", zap.Int("batchSize", len(items)))
				p.processItems(ctx, items)
				items = items[:0]
			}
			timer.Reset(timeout)

		case <-p.App.Context().Done():
			p.logger.Info("Shutting down processor, final items processing", zap.Int("finalBatchSize", len(items)))
			break loop
		}
	}

	if len(items) > 0 {
		p.logger.Info("Final batch processing after shutdown", zap.Int("batchSize", len(items)))
		p.processItems(ctx, items)
	}

	if len(items) > 0 {
		p.logger.Info("Final batch processing after shutdown", zap.Int("batchSize", len(items)))
		p.processItems(ctx, items)
	}
}

func (p *Processor[T]) processItems(ctx context.Context, items []*T) {
	p.logger.Info("Processing batch of items", zap.Int("batchSize", len(items)))

	if ctx.Err() != nil {
		p.logger.Error("Context canceled before processing", zap.Error(ctx.Err()))
		return
	}

}

type ProcessorThread[T any, P any] struct {
	*Processor[T]
	projects map[uint32]*org.Project
	digest   *xxhash.Digest
}

func NewProcessorThread[T any, P any](processor *Processor[T]) *ProcessorThread[T, P] {
	return &ProcessorThread[T, P]{
		Processor: processor,
		projects:  make(map[uint32]*org.Project),
		digest:    xxhash.New(),
	}
}

func (p *ProcessorThread[T, P]) project(ctx context.Context, projectID uint32) (*org.Project, bool) {
	if project, ok := p.projects[projectID]; ok {
		return project, true
	}

	project, err := org.SelectProject(ctx, p.App, projectID)
	if err != nil {
		p.App.Logger.Error("SelectProject failed", zap.Error(err))
		return nil, false
	}

	p.projects[projectID] = project
	return project, true
}

func (p *ProcessorThread[T, P]) forceName(ctx context.Context, item *T, getAttrs func(*T) map[string]interface{}, getProjectID func(*T) uint32, getEventName func(*T) string) bool {
	if getEventName(item) != "" {
		return false
	}

	project, ok := p.project(ctx, getProjectID(item))
	if !ok {
		return false
	}

	if libName, _ := getAttrs(item)["otel_library_name"].(string); libName != "" {
		return slices.Contains(project.ForceSpanName, libName)
	}
	return false
}