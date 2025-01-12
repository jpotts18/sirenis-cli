package prompt

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/elastic/go-elasticsearch"
	openai "github.com/openai/openai-go"
	"github.com/openai/openai-go/shared"
)

func Start(client *openai.Client) error {

    // Configure logging
    log.SetFlags(log.LstdFlags | log.Lmicroseconds)
    log.SetPrefix("[DEBUG] ")

    es, err := elasticsearch.NewDefaultClient()
    if err != nil {
        return fmt.Errorf("error creating Elasticsearch client: %w", err)
    }

    reader := bufio.NewReader(os.Stdin)
	fmt.Println("Enter your question (type 'exit' to quit):")

	for {
		fmt.Print("> ")
		question, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("error reading input: %w", err)
		}
		question = strings.TrimSpace(question)
		if question == "exit" {
			break
		}

		// Process the question
		err = processQuestion(client, es, question)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error processing question: %v\n", err)
		}
	}

    return nil
}

func processQuestion(client *openai.Client, es *elasticsearch.Client, question string) error {
    startTime := time.Now()
    log.Printf("Processing question: %q", question)

    // Generate embedding for the question
    resp, err := client.Embeddings.New(context.Background(), openai.EmbeddingNewParams{
        Input: openai.F[openai.EmbeddingNewParamsInputUnion](shared.UnionString(question)),
        Model: openai.F(openai.EmbeddingModelTextEmbeddingAda002),
    })
    if err != nil {
        return fmt.Errorf("error generating question embedding: %w", err)
    }
    questionEmbedding := resp.Data[0].Embedding
    log.Printf("Question embedded successfully (vector size: %d)", len(questionEmbedding))


    log.Println("Searching for relevant document chunks...")
    results, maxScore, err := searchChunks(es, questionEmbedding)
    if err != nil {
        return fmt.Errorf("error searching for chunks: %w", err)
    }
    log.Printf("Retrieved %d document chunks (highest similarity score: %.4f)", len(results), maxScore)


    var contextText string
    for i, result := range results {
        log.Printf("Chunk %d - Heading: %q", i+1, result["heading"])
        contextText += result["content"].(string) + "\n"
    }

    log.Printf("Generating Answer...")
    answer, err := generateAnswer(client, contextText, question)
    if err != nil {
        return fmt.Errorf("error generating answer: %w", err)
    }

    // Display the answer
    fmt.Println("\nAnswer:")
    fmt.Println(answer)
    fmt.Println()


    elapsed := time.Since(startTime)
    log.Printf("Total processing time: %v", elapsed)
    return nil
}

func searchChunks(es *elasticsearch.Client, questionEmbedding []float64) ([]map[string]interface{}, float64, error) {
    // Construct the search query
    query := map[string]interface{}{
        "size": 3, // Get top 3 most relevant chunks
        "query": map[string]interface{}{
            "script_score": map[string]interface{}{
                "query": map[string]interface{}{
                    "match_all": map[string]interface{}{},
                },
                "script": map[string]interface{}{
                    "source": "cosineSimilarity(params.query_vector, 'embedding') + 1.0",
                    "params": map[string]interface{}{
                        "query_vector": questionEmbedding,
                    },
                },
            },
        },
        "_source": []string{"heading", "content"}, // Only return these fields
    }

    // Convert query to JSON
    queryJSON, err := json.Marshal(query)
    if err != nil {
        return nil, 0, fmt.Errorf("error marshalling query: %w", err)
    }

    // Perform the search
    res, err := es.Search(
        es.Search.WithIndex("chunks"),
        es.Search.WithBody(strings.NewReader(string(queryJSON))),
    )
    if err != nil {
        return nil, 0, fmt.Errorf("error performing search: %w", err)
    }
    defer res.Body.Close()

    if res.IsError() {
        return nil, 0, fmt.Errorf("search error: %s", res.String())
    }

    // Parse the response
    var result map[string]interface{}
    if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
        return nil, 0, fmt.Errorf("error parsing response: %w", err)
    }

    // Extract hits
    hits, ok := result["hits"].(map[string]interface{})
    if !ok {
        return nil, 0, fmt.Errorf("unexpected response format")
    }

    hitsArray, ok := hits["hits"].([]interface{})
    if !ok {
        return nil, 0, fmt.Errorf("unexpected hits format")
    }

    // Extract source documents
    var chunks []map[string]interface{}
    var maxScore float64
    for _, hit := range hitsArray {
        hitMap, ok := hit.(map[string]interface{})
        if !ok {
            continue
        }
        if score, ok := hitMap["_source"].(float64); ok {
            if score > maxScore {
                maxScore = score
            }
        }
        source, ok := hitMap["_source"].(map[string]interface{})
        chunks = append(chunks, source)
    }

    return chunks, maxScore -1.0, nil
}


func generateAnswer(client *openai.Client, contextText, question string) (string, error) {
    // Create a more sophisticated system prompt that guides the model's behavior
    systemPrompt := `You are a knowledgeable assistant with expertise in the provided documentation. 
Your task is to:
1. Answer questions based ONLY on the provided context
2. If the context doesn't contain enough information, say so
3. Be concise but thorough
4. Use direct quotes from the context when relevant`

    // Format the user prompt to include clear instructions
    userPrompt := fmt.Sprintf(`Reference this context to answer the question:
---
%s
---

Question: %s

Please provide an accurate and relevant answer based solely on the information in the context above.`, 
        contextText, question)

        chatCompletion, err := client.Chat.Completions.New(context.TODO(), openai.ChatCompletionNewParams{
            Messages: openai.F([]openai.ChatCompletionMessageParamUnion{
                 openai.UserMessage(userPrompt),
                 openai.SystemMessage(systemPrompt),
            }),
            Model: openai.F(openai.ChatModelGPT4o),
            MaxTokens: openai.Int(500),
            Temperature: openai.F(0.3),
        })
    if err != nil {
        return "", fmt.Errorf("error generating completion: %w", err)
    }

    if len(chatCompletion.Choices) == 0 {
        return "", fmt.Errorf("no response generated")
    }

    answer := strings.TrimSpace(chatCompletion.Choices[0].Message.Content)
    return answer, nil
}
