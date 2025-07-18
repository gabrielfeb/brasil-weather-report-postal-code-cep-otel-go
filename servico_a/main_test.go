package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace/noop"
)

func TestMain(m *testing.M) {
	//Cria um provedor de tracer "noop".
	provider := noop.NewTracerProvider()
	otel.SetTracerProvider(provider)

	//**Passo crucial:** Inicializa a variável global `tracer`
	// que é usada pelos handlers. Sem isso, ela permanece nula.
	tracer = provider.Tracer("test-tracer")

	//Roda todos os testes do pacote.
	os.Exit(m.Run())
}

// Teste para a função de validação de CEP
func TestIsValidCep_ServiceA(t *testing.T) {
	//Tabela de casos de teste
	testCases := []struct {
		name     string
		cep      string
		expected bool
	}{
		{"CEP Válido", "12345678", true},
		{"CEP Inválido (curto)", "12345", false},
		{"CEP Inválido (longo)", "123456789", false},
		{"CEP Inválido (letras)", "1234567a", false},
		{"CEP Vazio", "", false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, isValidCep(tc.cep))
		})
	}
}

// Teste para o handler principal
func TestHandleCepRequest(t *testing.T) {

	//Cria um servidor de teste para simular o Serviço B
	mockServiceB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"city":"Test City","temp_C":25.0,"temp_F":77.0,"temp_K":298.0}`))
	}))
	defer mockServiceB.Close()

	//Define a variável de ambiente para que o Serviço A aponte para o mock
	os.Setenv("SERVICE_B_URL", mockServiceB.URL)
	defer os.Unsetenv("SERVICE_B_URL")

	t.Run("Cenário de Sucesso com CEP válido", func(t *testing.T) {
		//Cria o corpo da requisição com um CEP válido
		requestBody := bytes.NewBuffer([]byte(`{"cep": "01001000"}`))
		req := httptest.NewRequest(http.MethodPost, "/", requestBody)
		rr := httptest.NewRecorder() // Grava a resposta

		handleCepRequest(rr, req)

		//Verifica se o status code é OK (200)
		assert.Equal(t, http.StatusOK, rr.Code)
		//Verifica se a resposta é o que o mock do Serviço B retornou
		assert.JSONEq(t, `{"city":"Test City","temp_C":25.0,"temp_F":77.0,"temp_K":298.0}`, rr.Body.String())
	})

	t.Run("Cenário de Falha com CEP inválido", func(t *testing.T) {
		//Cria o corpo da requisição com um CEP inválido
		requestBody := bytes.NewBuffer([]byte(`{"cep": "123"}`))
		req := httptest.NewRequest(http.MethodPost, "/", requestBody)
		rr := httptest.NewRecorder()

		handleCepRequest(rr, req)

		//Verifica se o status code é 422
		assert.Equal(t, http.StatusUnprocessableEntity, rr.Code)
		//Verifica a mensagem de erro
		assert.Equal(t, "invalid zipcode", rr.Body.String())
	})

	t.Run("Cenário de Falha com método HTTP inválido", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rr := httptest.NewRecorder()

		handleCepRequest(rr, req)

		//Verifica se o status code é 405
		assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
	})
}
