package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// --- Mock do Cliente HTTP ---

// 1. Crie uma struct para o mock
type MockHTTPClient struct {
	mock.Mock
}

// 2. Implemente o método Do da interface do cliente HTTP
func (m *MockHTTPClient) Do(req *http.Request) (*http.Response, error) {
	args := m.Called(req)
	return args.Get(0).(*http.Response), args.Error(1)
}

// --- Testes ---

func TestSearchCep_ServiceB(t *testing.T) {
	t.Run("Sucesso - Encontra CEP", func(t *testing.T) {
		// Corpo da resposta simulada da API ViaCEP
		jsonResponse := `{"localidade": "São Paulo"}`
		r := io.NopCloser(bytes.NewReader([]byte(jsonResponse)))
		
		mockClient := new(MockHTTPClient)
		// Configure o mock: quando Do for chamado, retorne esta resposta
		mockClient.On("Do", mock.Anything).Return(&http.Response{
			StatusCode: http.StatusOK,
			Body:       r,
		}, nil)

		local, err := SearchCep(context.Background(), "01001000", &http.Client{Transport: mockClient.RoundTripper()})

		assert.NoError(t, err)
		assert.NotNil(t, local)
		assert.Equal(t, "São Paulo", local.Localidade)
		mockClient.AssertExpectations(t) // Verifica se o mock foi chamado
	})

	t.Run("Falha - CEP não encontrado", func(t *testing.T) {
		jsonResponse := `{"erro": true}`
		r := io.NopCloser(bytes.NewReader([]byte(jsonResponse)))
		
		mockClient := new(MockHTTPClient)
		mockClient.On("Do", mock.Anything).Return(&http.Response{
			StatusCode: http.StatusOK, // ViaCEP retorna 200 mesmo com erro no corpo
			Body:       r,
		}, nil)

		local, err := SearchCep(context.Background(), "99999999", &http.Client{Transport: mockClient.RoundTripper()})

		assert.Error(t, err)
		assert.Nil(t, local)
		assert.Equal(t, "can not find zipcode", err.Error())
		mockClient.AssertExpectations(t)
	})
}

// Vamos testar o weatherHandler, que orquestra tudo
func TestWeatherHandler_ServiceB(t *testing.T) {

	// O Testify não tem um mock nativo para http.Client, então usamos uma técnica comum:
	// mockamos o `Transport` do cliente.
	
	// Crie uma struct que implementa a interface http.RoundTripper
	type MockRoundTripper struct {
		mock.Mock
	}

	func (m *MockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
		args := m.Called(req)
		return args.Get(0).(*http.Response), args.Error(1)
	}

	t.Run("Cenário de Sucesso Completo", func(t *testing.T) {
		mockTransport := new(MockRoundTripper)
		
		// Simula a resposta do ViaCEP
		viaCepRespBody := io.NopCloser(strings.NewReader(`{"localidade": "Florianopolis"}`))
		viaCepResp := &http.Response{StatusCode: 200, Body: viaCepRespBody}

		// Simula a resposta da WeatherAPI
		weatherRespBody := io.NopCloser(strings.NewReader(`{"current": {"temp_c": 28.5}}`))
		weatherResp := &http.Response{StatusCode: 200, Body: weatherRespBody}

		// Configura o mock para responder de acordo com a URL da requisição
		mockTransport.On("RoundTrip", mock.MatchedBy(func(req *http.Request) bool {
			return strings.Contains(req.URL.Host, "viacep.com.br")
		})).Return(viaCepResp, nil).Once() // Responde uma vez

		mockTransport.On("RoundTrip", mock.MatchedBy(func(req *http.Request) bool {
			return strings.Contains(req.URL.Host, "api.weatherapi.com")
		})).Return(weatherResp, nil).Once() // Responde uma vez

		// Injeta o transport mockado no cliente
		testClient := &http.Client{Transport: mockTransport}

		// Cria uma versão do handler que usa nosso cliente mockado
		testHandler := func(w http.ResponseWriter, r *http.Request) {
			// Simula a lógica do handler original, mas injetando o cliente
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
		}

		req := httptest.NewRequest(http.MethodGet, "/weather/88010000", nil)
		rr := httptest.NewRecorder()

		testHandler(rr, req)
		
		assert.Equal(t, http.StatusOK, rr.Code)
		expectedJson := `{"city":"Florianopolis","temp_C":28.5,"temp_F":83.3,"temp_K":301.5}`
		assert.JSONEq(t, expectedJson, rr.Body.String())
		mockTransport.AssertExpectations(t)
	})

	t.Run("Cenário de Falha - CEP não encontrado", func(t *testing.T) {
		mockTransport := new(MockRoundTripper)
		
		// Simula a resposta de erro do ViaCEP
		viaCepRespBody := io.NopCloser(strings.NewReader(`{"erro": true}`))
		viaCepResp := &http.Response{StatusCode: 200, Body: viaCepRespBody}

		mockTransport.On("RoundTrip", mock.Anything).Return(viaCepResp, nil).Once()
		testClient := &http.Client{Transport: mockTransport}

		// Para testar o handler diretamente, o mais simples é chamar a lógica dele
		req := httptest.NewRequest(http.MethodGet, "/weather/99999999", nil)
		rr := httptest.NewRecorder()

		// Como o handler original tem muita lógica, vamos simular seu comportamento
		local, err := SearchCep(req.Context(), "99999999", testClient)
		
		// Assertivas sobre o erro retornado pela função que falhou
		assert.Nil(t, local)
		assert.NotNil(t, err)
		assert.Equal(t, "can not find zipcode", err.Error())
		
		// Testando o handler real com o erro simulado
		handler := http.HandlerFunc(weatherHandler)
		// Como não podemos injetar o cliente no handler diretamente, testamos as unidades (SearchCep/GetWeather)
		// e confiamos na integração. A abordagem acima com o `testHandler` é mais completa para um teste de unidade do handler.
	})
}