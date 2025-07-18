package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"syscall"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var tracer trace.Tracer

// initTracer inicializa o OpenTelemetry Tracer
func initTracer(serviceName string) (*sdktrace.TracerProvider, error) {
	ctx := context.Background()
	otelAgentAddr, ok := os.LookupEnv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if !ok {
		otelAgentAddr = "otel-collector:4317"
	}

	conn, err := grpc.NewClient(otelAgentAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC connection to collector: %w", err)
	}

	traceExporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithGRPCConn(conn))
	if err != nil {
		return nil, fmt.Errorf("failed to create trace exporter: %w", err)
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String(serviceName),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	bsp := sdktrace.NewBatchSpanProcessor(traceExporter)
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithResource(res),
		sdktrace.WithSpanProcessor(bsp),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	tracer = otel.Tracer(serviceName)
	return tp, nil
}

// CepInput é a struct para o corpo da requisição de entrada
type CepInput struct {
	Cep string `json:"cep"`
}

func main() {
	tp, err := initTracer("service-a")
	if err != nil {
		log.Fatalf("failed to initialize tracer: %v", err)
	}
	defer func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			log.Printf("Error shutting down tracer provider: %v", err)
		}
	}()

	mux := http.NewServeMux()
	// Usando otelhttp.NewHandler para instrumentar o handler
	handler := otelhttp.NewHandler(http.HandlerFunc(handleCepRequest), "CepHandler")
	mux.Handle("/", handler)

	log.Println("Service A started on port 8080")
	go func() {
		if err := http.ListenAndServe(":8080", mux); err != nil && err != http.ErrServerClosed {
			log.Fatalf("ListenAndServe failed: %v", err)
		}
	}()

	// Graceful shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	log.Println("Shutting down server...")
}

func handleCepRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extrai o contexto e o tracer para criar um span
	ctx := r.Context()
	_, span := tracer.Start(ctx, "handleCepRequest")
	defer span.End()

	var input CepInput
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Error reading request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	if err := json.Unmarshal(body, &input); err != nil {
		http.Error(w, "Error decoding JSON", http.StatusBadRequest)
		return
	}

	// Validação do CEP
	if !isValidCep(input.Cep) {
		w.WriteHeader(http.StatusUnprocessableEntity) // 422
		w.Write([]byte("invalid zipcode"))
		return
	}

	// Encaminha para o Serviço B
	forwardRequestToServiceB(w, r, input.Cep)
}

func isValidCep(cep string) bool {
	// Regex para validar se a string contém exatamente 8 dígitos
	match, _ := regexp.MatchString(`^\d{8}$`, cep)
	return match
}

func forwardRequestToServiceB(w http.ResponseWriter, r *http.Request, cep string) {
	// Extrai o contexto para propagar o trace
	ctx := r.Context()
	_, span := tracer.Start(ctx, "forwardRequestToServiceB")
	defer span.End()

	serviceB_URL, ok := os.LookupEnv("SERVICE_B_URL")
	if !ok {
		serviceB_URL = "http://service-b:8081"
	}

	targetURL := fmt.Sprintf("%s/weather/%s", serviceB_URL, cep)

	// Cria um cliente HTTP instrumentado pelo OTEL
	client := http.Client{
		Transport: otelhttp.NewTransport(http.DefaultTransport),
		Timeout:   10 * time.Second,
	}

	req, err := http.NewRequestWithContext(ctx, "GET", targetURL, nil)
	if err != nil {
		http.Error(w, "Failed to create request for service B", http.StatusInternalServerError)
		return
	}

	// A propagação do trace é feita automaticamente pelo otelhttp
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "Failed to call service B", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	// Repassa a resposta do Serviço B para o cliente original
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}
