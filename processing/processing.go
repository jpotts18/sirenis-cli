package processing

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/elastic/go-elasticsearch"
	"github.com/elastic/go-elasticsearch/esapi"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/shared"
	"github.com/russross/blackfriday/v2"
)


type Chunk struct {
    Heading   string
    Content   string
    Level     int
    Embedding []float64
}


func ImportFile(client *openai.Client, filePath string) error {
    content, err := os.ReadFile(filePath)
    if err != nil {
        return fmt.Errorf("failed to read file: %w", err)
    }

    md := blackfriday.New()
    node := md.Parse(content)

    // Chunk the document
    var chunks []Chunk
    var currentChunk *Chunk

    node.Walk(func(n *blackfriday.Node, entering bool) blackfriday.WalkStatus {
        if entering {
            if n.Type == blackfriday.Heading {
                level := n.HeadingData.Level
                headingText := ""
                for c := n.FirstChild; c != nil; c = c.Next {
                    headingText += string(c.Literal)
                }
                if level == 2 {
                    if currentChunk != nil {
                        chunks = append(chunks, *currentChunk)
                    }
                    currentChunk = &Chunk{
                        Heading: headingText,
                        Level:   level,
                        Content: "",
                    }
                }
            } else if n.Type == blackfriday.Paragraph {
                paragraphText := ""
                for c := n.FirstChild; c != nil; c = c.Next {
                    paragraphText += string(c.Literal)
                }
                if currentChunk != nil {
                    currentChunk.Content += paragraphText + "\n"
                }
            }
        }
        return blackfriday.GoToNext
    })

    // Add the last chunk
    if currentChunk != nil {
        chunks = append(chunks, *currentChunk)
    }

    // Generate embeddings

    for i, chunk := range chunks {
        resp, err := client.Embeddings.New(context.Background(), openai.EmbeddingNewParams{
            Input: openai.F[openai.EmbeddingNewParamsInputUnion](shared.UnionString(chunk.Content)),
            Model: openai.F(openai.EmbeddingModelTextEmbeddingAda002),
        })
        if err != nil {
            return fmt.Errorf("error generating embeddigns: %w", err)
        }
        embedding := resp.Data[0].Embedding
        chunks[i].Embedding = embedding
    }
    // Store embeddings in Elasticsearch
    es, err := elasticsearch.NewDefaultClient()
    if err != nil {
        return fmt.Errorf("error creating Elasticsearch client: %w", err)
    }

    // Check if index exists
    existsRes, err := es.Indices.Exists([]string{"chunks"})
    if err != nil {
        return fmt.Errorf("error checking if index exists: %w", err)
    }
    defer existsRes.Body.Close()

    if existsRes.StatusCode == 404 {
        // Create index
        if err := createIndex(es); err != nil {
            return fmt.Errorf("error creating index: %w", err)
        }
    }

    if err := storeChunks(es, chunks); err != nil {
        return fmt.Errorf("error storing chunks: %w", err)
    }

    fmt.Println("File imported and processed successfully.")
    return nil
}


func createIndex(es *elasticsearch.Client) error {
    mapping := `{
      "mappings": {
        "properties": {
          "heading": {
            "type": "text"
          },
          "content": {
            "type": "text"
          },
          "embedding": {
            "type": "dense_vector",
            "dims": 1536
          }
        }
      }
    }`

    res, err := es.Indices.Create("chunks", es.Indices.Create.WithBody(strings.NewReader(mapping)))
    if err != nil {
        return fmt.Errorf("error creating index: %w", err)
    }
    defer res.Body.Close()

    if res.IsError() {
        return fmt.Errorf("error response from Elasticsearch: %s", res.String())
    }

    return nil
}

func storeChunks(es *elasticsearch.Client, chunks []Chunk) error {
    for i, chunk := range chunks {
        doc := map[string]interface{}{
            "heading":   chunk.Heading,
            "content":   chunk.Content,
            "embedding": chunk.Embedding,
        }

        jsonData, err := json.Marshal(doc)
        if err != nil {
            return fmt.Errorf("error marshalling JSON: %w", err)
        }

        req := esapi.IndexRequest{
            Index:      "chunks",
            DocumentID: strconv.Itoa(i), // Use a unique ID
            Body:       strings.NewReader(string(jsonData)),
            Refresh:    "true",
        }

        res, err := req.Do(context.Background(), es)
        if err != nil {
            return fmt.Errorf("error indexing document: %w", err)
        }
        defer res.Body.Close()

        if res.IsError() {
            log.Printf("[%s] Error indexing document ID=%d", res.Status(), i)
        } else {
            log.Printf("[%s] Document ID=%d indexed.", res.Status(), i)
        }
    }
    return nil
}
