#!/usr/bin/env python3
"""Polish embedding server using sdadas/mmlw-roberta-large"""
from fastapi import FastAPI
from pydantic import BaseModel
from sentence_transformers import SentenceTransformer
import uvicorn

app = FastAPI()
model = None

class EmbedRequest(BaseModel):
    input: str | list[str]
    model: str = "mmlw-roberta-large"

@app.on_event("startup")
def load_model():
    global model
    print("Loading sdadas/mmlw-roberta-large...")
    model = SentenceTransformer("sdadas/mmlw-roberta-large")
    print("Model loaded!")

@app.post("/v1/embeddings")
def embed(req: EmbedRequest):
    texts = [req.input] if isinstance(req.input, str) else req.input
    # Add query prefix as recommended for this model
    texts = [f"zapytanie: {t}" for t in texts]
    embeddings = model.encode(texts).tolist()
    return {
        "object": "list",
        "data": [{"object": "embedding", "embedding": e, "index": i} for i, e in enumerate(embeddings)],
        "model": "mmlw-roberta-large",
        "usage": {"prompt_tokens": sum(len(t.split()) for t in texts), "total_tokens": sum(len(t.split()) for t in texts)}
    }

@app.get("/v1/models")
def models():
    return {"data": [{"id": "mmlw-roberta-large", "object": "model"}]}

if __name__ == "__main__":
    uvicorn.run(app, host="127.0.0.1", port=1235)
