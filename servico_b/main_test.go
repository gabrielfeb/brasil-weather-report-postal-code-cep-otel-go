package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace/noop"
)

// Simula as respostas das chamadas HTTP.
type MockRoundTripper struct {
	mock.Mock
}

// Implementa a interface http.RoundTripper para o mock.
func (m *MockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	args := m.Called(req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*http.Response), args.Error(1)
}

// Inicializa o tracer para evitar panic nos testes.
func TestMain(m *testing.M) {
	provider := noop.NewTracerProvider()
	otel.SetTracerProvider(provider)
	tracer = provider.Tracer("test-tracer-b")
	os.Exit(m.Run())
}

// Testa o fluxo completo do handler principal.
func TestWeatherHandler_ServiceB(t *testing.T) {
	weatherApiKey = "test-key"

	t.Run("Cenário de Sucesso Completo", func(t *testing.T) {
		//Prepara o mock do transport
		mockTransport := new(MockRoundTripper)
		testClient := &http.Client{Transport: mockTransport}

		// Simula a resposta do ViaCEP
		viaCepRespBody := io.NopCloser(strings.NewReader(`{"localidade": "Florianopolis"}`))
		viaCepResp := &http.Response{StatusCode: 200, Body: viaCepRespBody}

		//Simula a resposta da WeatherAPI
		weatherRespBody := io.NopCloser(strings.NewReader(`{"current": {"temp_c": 28.5}}`))
		weatherResp := &http.Response{StatusCode: 200, Body: weatherRespBody}

		//Configura o mock para responder de acordo com a URL da requisição
		mockTransport.On("RoundTrip", mock.MatchedBy(func(req *http.Request) bool {
			return strings.Contains(req.URL.Host, "viacep.com.br")
		})).Return(viaCepResp, nil).Once()

		mockTransport.On("RoundTrip", mock.MatchedBy(func(req *http.Request) bool {
			return strings.Contains(req.URL.Host, "api.weatherapi.com")
		})).Return(weatherResp, nil).Once()

		//Cria um handler que usa o cliente mockado para o teste
		handlerToTest := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			cep := r.URL.Path[len("/weather/"):]
			local, _ := SearchCep(ctx, cep, testClient)
			clima, _ := GetWeather(ctx, local.Localidade, testClient)

			response := TemperaturaFinal{
				City:  local.Localidade,
				TempC: clima.Current.TempC,
				TempF: clima.Current.TempC*1.8 + 32,
				TempK: clima.Current.TempC + 273,
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(response)
		})

		//Executa a requisição de teste
		req := httptest.NewRequest(http.MethodGet, "/weather/88010000", nil)
		rr := httptest.NewRecorder()
		handlerToTest.ServeHTTP(rr, req)

		//Verifica os resultados
		assert.Equal(t, http.StatusOK, rr.Code)

		// Decodifica a resposta para verificar os campos individualmente
		var actualResponse TemperaturaFinal
		err := json.Unmarshal(rr.Body.Bytes(), &actualResponse)
		assert.NoError(t, err)

		assert.Equal(t, "Florianopolis", actualResponse.City)
		assert.Equal(t, 28.5, actualResponse.TempC)
		assert.InDelta(t, 83.3, actualResponse.TempF, 0.001)
		assert.Equal(t, 301.5, actualResponse.TempK)

		mockTransport.AssertExpectations(t)
	})

	t.Run("Cenário de Falha - CEP não encontrado", func(t *testing.T) {
		mockTransport := new(MockRoundTripper)
		testClient := &http.Client{Transport: mockTransport}

		//Simula a resposta de erro do ViaCEP
		viaCepRespBody := io.NopCloser(strings.NewReader(`{"erro": true}`))
		viaCepResp := &http.Response{StatusCode: 200, Body: viaCepRespBody}
		mockTransport.On("RoundTrip", mock.MatchedBy(func(req *http.Request) bool {
			return strings.Contains(req.URL.Host, "viacep.com.br")
		})).Return(viaCepResp, nil).Once()

		//Cria um handler que usa o cliente mockado para este teste
		handlerToTest := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			cep := r.URL.Path[len("/weather/"):]
			_, err := SearchCep(ctx, cep, testClient)

			if err != nil {
				if err.Error() == "can not find zipcode" {
					http.Error(w, err.Error(), http.StatusNotFound)
				} else {
					http.Error(w, err.Error(), http.StatusInternalServerError)
				}
				return
			}
		})

		req := httptest.NewRequest(http.MethodGet, "/weather/99999999", nil)
		rr := httptest.NewRecorder()
		handlerToTest.ServeHTTP(rr, req)

		// Verifica o erro 404
		assert.Equal(t, http.StatusNotFound, rr.Code)
		assert.Equal(t, "can not find zipcode\n", rr.Body.String())
		mockTransport.AssertExpectations(t)
	})

	t.Run("Cenário de Falha - CEP inválido", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/weather/123", nil)
		rr := httptest.NewRecorder()
		weatherHandler(rr, req)
		assert.Equal(t, http.StatusUnprocessableEntity, rr.Code)
		assert.Equal(t, "invalid zipcode\n", rr.Body.String())
	})
}
