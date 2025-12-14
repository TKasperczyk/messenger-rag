import { MilvusClient } from '@zilliz/milvus2-sdk-node';

const milvusAddr = "localhost:19530";
const collection = "messenger_message_chunks_v2";
const lmstudioUrl = "http://127.0.0.1:1234/v1";
const embedModel = "text-embedding-qwen3-embedding-8b";

async function getEmbedding(text) {
    const res = await fetch(`${lmstudioUrl}/embeddings`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ model: embedModel, input: text })
    });
    const data = await res.json();
    return data.data[0].embedding;
}

async function main() {
    const client = new MilvusClient({ address: milvusAddr });

    // Get a specific cat chunk from our earlier query
    console.log("1. Searching for cat-related chunks by filtering on text...");

    // Search with a query that SHOULD find cat content
    const query = "kot gruby tłusty kotek";  // fat chubby cat in Polish
    const emb = await getEmbedding(query);

    const results = await client.search({
        collection_name: collection,
        data: [emb],
        anns_field: 'embedding',
        metric_type: 'COSINE',
        params: { ef: 500 },
        limit: 50,
        output_fields: ['chunk_id', 'thread_name', 'text']
    });

    // Filter to only show cat-related results
    console.log(`\nQuery: "${query}"`);
    console.log("Results containing 'kot', 'cat', or 'kotek':");
    console.log("=" .repeat(80));

    let catCount = 0;
    for (const r of results.results) {
        const text = String(r.text || '');
        if (/kot|cat|kotek/i.test(text)) {
            catCount++;
            const preview = text.slice(0, 150).replace(/\n/g, ' ');
            const score = (r.score * 100).toFixed(1);
            console.log(`[${score}%] ${r.thread_name}: "${preview}..."`);
            if (catCount >= 10) break;
        }
    }

    if (catCount === 0) {
        console.log("No cat-related chunks found in top 50 results!");
    }

    // Let's also check the embedding similarity of a known cat text
    console.log("\n\n2. Testing embedding similarity of cat content vs 9gag content...");

    const catText = "[Wojciech Kozioł]: A co do kotów to zmarł James Cathcart. Aktor głosowy kota z zespołu R";
    const gag9Text = "Join the fun convo with 9GAG community http://9gag.com/gag/amBd8Qo";

    const catEmb = await getEmbedding(catText);
    const gag9Emb = await getEmbedding(gag9Text);
    const queryEmb = await getEmbedding("kot bob chonk");

    function cosineSim(a, b) {
        let dot = 0, normA = 0, normB = 0;
        for (let i = 0; i < a.length; i++) {
            dot += a[i] * b[i];
            normA += a[i] * a[i];
            normB += b[i] * b[i];
        }
        return dot / (Math.sqrt(normA) * Math.sqrt(normB));
    }

    console.log(`\nQuery: "kot bob chonk"`);
    console.log(`  vs cat text:  ${(cosineSim(queryEmb, catEmb) * 100).toFixed(1)}%`);
    console.log(`  vs 9gag text: ${(cosineSim(queryEmb, gag9Emb) * 100).toFixed(1)}%`);

    // Check if the cat content exists in Milvus
    console.log("\n\n3. Checking if cat chunks are in Milvus (by chunk_id)...");

    // Known cat chunk IDs from our SQLite query
    const catChunkIds = [
        "8852a4a179921eb7",  // kotów zmarł James Cathcart
        "cab8163bcc4006b0",  // nowego kota
        "63a73d359b964ea1",  // kot musi ruszać
        "4da7b017d11bab73",  // mruczenie kota
    ];

    for (const chunkId of catChunkIds) {
        const results = await client.query({
            collection_name: collection,
            filter: `chunk_id == "${chunkId}"`,
            output_fields: ['chunk_id', 'text'],
            limit: 1
        });

        if (results.data.length > 0) {
            const text = String(results.data[0].text).slice(0, 80).replace(/\n/g, ' ');
            console.log(`  ✓ Found ${chunkId}: "${text}..."`);
        } else {
            console.log(`  ✗ NOT FOUND: ${chunkId}`);
        }
    }

    await client.closeConnection();
}

main().catch(console.error);
