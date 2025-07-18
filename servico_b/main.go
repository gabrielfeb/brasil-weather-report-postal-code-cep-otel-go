package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"syscall"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	tracer        trace.Tracer
	weatherApiKey string
)

// Local representa a resposta da API ViaCEP
type Local struct {
	Localidade string `json:"localidade"`
	Erro       bool   `json:"erro"`
}

// Clima representa a resposta da WeatherAPI
type Clima struct {
	Current struct {
		TempC float64 `json:"temp_c"`
	} `json:"current"`
}

// TemperaturaFinal é a struct final da resposta, conforme solicitado
type TemperaturaFinal struct {
	City  string  `json:"city"`
	TempC float64 `json:"temp_C"`
	TempF float64 `json:"temp_F"`
	TempK float64 `json:"temp_K"`
}

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

func main() {
	var ok bool
	weatherApiKey, ok = os.LookupEnv("WEATHER_API_KEY")
	if !ok || weatherApiKey == "" {
		log.Fatal("WEATHER_API_KEY environment variable not set")
	}

	tp, err := initTracer("service-b")
	if err != nil {
		log.Fatalf("failed to initialize tracer: %v", err)
	}
	defer func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			log.Printf("Error shutting down tracer provider: %v", err)
		}
	}()

	mux := http.NewServeMux()
	handler := otelhttp.NewHandler(http.HandlerFunc(weatherHandler), "WeatherHandler")
	mux.Handle("/weather/", handler)

	log.Println("Service B started on port 8081")
	go func() {
		if err := http.ListenAndServe(":8081", mux); err != nil && err != http.ErrServerClosed {
			log.Fatalf("ListenAndServe failed: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	log.Println("Shutting down server...")
}

// weatherHandler orquestra a lógica principal
func weatherHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_, span := tracer.Start(ctx, "weatherHandler-orchestration")
	defer span.End()

	cep := r.URL.Path[len("/weather/"):]

	if !isValidCep(cep) {
		http.Error(w, "invalid zipcode", http.StatusUnprocessableEntity)
		return
	}
	span.SetAttributes(attribute.String("cep.input", cep))

	local, err := SearchCep(ctx, cep)
	if err != nil {
		if err.Error() == "can not find zipcode" {
			http.Error(w, err.Error(), http.StatusNotFound)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	span.SetAttributes(attribute.String("city.name", local.Localidade))

	clima, err := GetWeather(ctx, local.Localidade)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	span.SetAttributes(attribute.Float64("temperature.celsius", clima.Current.TempC))

	tempC := clima.Current.TempC
	tempF := tempC*1.8 + 32
	tempK := tempC + 273

	response := TemperaturaFinal{
		City:  local.Localidade,
		TempC: tempC,
		TempF: tempF,
		TempK: tempK,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

func isValidCep(cep string) bool {
	match, _ := regexp.MatchString(`^\d{8}$`, cep)
	return match
}

// SearchCep busca os dados de localização do CEP
func SearchCep(ctx context.Context, cep string) (*Local, error) {
	ctx, span := tracer.Start(ctx, "get-location-from-cep-api (SearchCep)")
	defer span.End()

	client := http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport), Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("https://viacep.com.br/ws/%s/json/", cep), nil)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}
	defer resp.Body.Close()

	var local Local
	if err := json.NewDecoder(resp.Body).Decode(&local); err != nil {
		span.RecordError(err)
		return nil, err
	}

	if local.Erro {
		return nil, fmt.Errorf("can not find zipcode")
	}

	return &local, nil
}

// GetWeather busca os dados de clima da cidade
func GetWeather(ctx context.Context, city string) (*Clima, error) {
	ctx, span := tracer.Start(ctx, "get-weather-from-weather-api (GetWeather)")
	defer span.End()

	client := http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport), Timeout: 5 * time.Second}
	encodedCity := url.QueryEscape(city)
	url := fmt.Sprintf("https://api.weatherapi.com/v1/current.json?key=%s&q=%s&aqi=no", weatherApiKey, encodedCity)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("weather API returned status %d", resp.StatusCode)
		span.RecordError(err)
		return nil, err
	}

	var clima Clima
	if err := json.NewDecoder(resp.Body).Decode(&clima); err != nil {
		span.RecordError(err)
		return nil, err
	}

	return &clima, nil
}
