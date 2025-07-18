# Sistema de Temperatura por CEP com OpenTelemetry

Este projeto consiste em dois microserviços em Go que trabalham em conjunto para fornecer a temperatura atual de uma cidade a partir de um CEP (Código de Endereçamento Postal) brasileiro. A comunicação e a observabilidade são monitoradas usando OpenTelemetry e Zipkin.

- **Serviço A**: Recebe o CEP via POST, valida e o encaminha para o Serviço B.
- **Serviço B**: Recebe o CEP, busca a cidade correspondente (ViaCEP), obtém a temperatura (WeatherAPI) e retorna os dados formatados.

## Arquitetura

```
[Cliente] -> POST / (com CEP) -> [Serviço A] -> GET /weather/{cep} -> [Serviço B]
                                     |                                     |
                                     |                                     +-> [ViaCEP API]
                                     |                                     +-> [WeatherAPI]
                                     |
                                     +-----> [OTEL Collector] -> [Zipkin]
```

## Pré-requisitos

- **Docker** e **Docker Compose** instalados.
- Uma **chave de API** da [WeatherAPI](https://www.weatherapi.com/). O plano gratuito é suficiente.

## Configuração

1.  **Clone o repositório:**
    ```bash
    git clone <URL_DO_SEU_REPOSITORIO>
    cd brasil-weather-report
    ```

2.  **Crie o arquivo de ambiente:**
    Crie um arquivo chamado `.env` na raiz do projeto.

3.  **Adicione sua chave da API:**
    Abra o arquivo `.env` e adicione sua chave da WeatherAPI da seguinte forma:
    ```
    WEATHER_API_KEY=SUA_CHAVE_AQUI
    ```
    Substitua `SUA_CHAVE_AQUI` pela sua chave real.

## Como Rodar o Projeto

Com o Docker em execução, suba todos os contêineres com um único comando:

```bash
docker-compose up --build
```

Os serviços estarão disponíveis nos seguintes endereços:

- **Serviço A**: `http://localhost:8080`
- **Serviço B**: `http://localhost:8081`
- **Zipkin UI**: `http://localhost:9411`

## Como Testar

Você pode usar `curl` ou qualquer cliente de API para testar os endpoints.

#### Cenário 1: Sucesso

Envie um CEP válido para o Serviço A.

```bash
curl -X POST http://localhost:8080 \
-H "Content-Type: application/json" \
-d '{"cep": "01001000"}' # CEP da Praça da Sé, São Paulo
```

**Resposta Esperada (HTTP 200):**
```json
{
    "city": "São Paulo",
    "temp_C": 21.0,
    "temp_F": 69.8,
    "temp_K": 294.0
}
```
*(Os valores de temperatura variarão)*

#### Cenário 2: CEP com formato inválido

Envie um CEP com menos de 8 dígitos ou contendo letras.

```bash
curl -i -X POST http://localhost:8080 \
-H "Content-Type: application/json" \
-d '{"cep": "12345"}'
```

**Resposta Esperada (HTTP 422):**
```
HTTP/1.1 422 Unprocessable Entity
Date: ...
Content-Length: 15
Content-Type: text/plain; charset=utf-8

invalid zipcode
```

#### Cenário 3: CEP não encontrado

Envie um CEP com 8 dígitos, mas que não existe.

```bash
curl -i -X POST http://localhost:8080 \
-H "Content-Type: application/json" \
-d '{"cep": "99999999"}'
```

**Resposta Esperada (HTTP 404):**
```
HTTP/1.1 404 Not Found
Date: ...
Content-Length: 21
Content-Type: text/plain; charset=utf-8

can not find zipcode
```

## Visualizando os Traces no Zipkin

1.  Após fazer algumas requisições, abra seu navegador e acesse a interface do Zipkin: `http://localhost:9411`.
2.  Clique no botão "Run Query" para ver os traces mais recentes.
3.  Você verá traces para `service-a`. Ao clicar em um deles, poderá ver a linha do tempo completa da requisição, incluindo:
    - O tempo total no `service-a`.
    - A chamada HTTP para o `service-b`.
    - Dentro do `service-b`, os spans específicos para `get-location-from-cep-api` e `get-weather-from-weather-api`, medindo o tempo gasto em cada chamada externa.