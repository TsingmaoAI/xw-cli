---
language:
- en
pipeline_tag: text-generation
tags:
- pretrained
license: apache-2.0
---

# Qwen2-7B

## Introduction

Qwen2 is the new series of Qwen large language models. For Qwen2, we release a number of base language models and instruction-tuned language models ranging from 0.5 to 72 billion parameters, including a Mixture-of-Experts model. This repo contains the 7B Qwen2 base language model.

Compared with the state-of-the-art opensource language models, including the previous released Qwen1.5, Qwen2 has generally surpassed most opensource models and demonstrated competitiveness against proprietary models across a series of benchmarks targeting for language understanding, language generation, multilingual capability, coding, mathematics, reasoning, etc.

For more details, please refer to our [blog](https://qwenlm.github.io/blog/qwen2/), [GitHub](https://github.com/QwenLM/Qwen2), and [Documentation](https://qwen.readthedocs.io/en/latest/).
<br>


## Model Details
Qwen2 is a language model series including decoder language models of different model sizes. For each size, we release the base language model and the aligned chat model. It is based on the Transformer architecture with SwiGLU activation, attention QKV bias, group query attention, etc. Additionally, we have an improved tokenizer adaptive to multiple natural languages and codes.

## Requirements
The code of Qwen2 has been in the latest Hugging face transformers and we advise you to install `transformers>=4.37.0`, or you might encounter the following error:
```
KeyError: 'qwen2'
```


## Usage

We do not advise you to use base language models for text generation. Instead, you can apply post-training, e.g., SFT, RLHF, continued pretraining, etc., on this model.


### Performance

The evaluation of base models mainly focuses on the model performance of natural language understanding, general question answering, coding, mathematics, scientific knowledge, reasoning, multilingual capability, etc. 

The datasets for evaluation include: 
 
**English Tasks**: MMLU (5-shot), MMLU-Pro (5-shot), GPQA (5shot), Theorem QA (5-shot), BBH (3-shot), HellaSwag (10-shot), Winogrande (5-shot), TruthfulQA (0-shot), ARC-C (25-shot)
 
**Coding Tasks**: EvalPlus (0-shot) (HumanEval, MBPP, HumanEval+, MBPP+), MultiPL-E (0-shot) (Python, C++, JAVA, PHP, TypeScript, C#, Bash, JavaScript)
  
**Math Tasks**: GSM8K (4-shot), MATH (4-shot)
 
**Chinese Tasks**: C-Eval(5-shot), CMMLU (5-shot)
 
**Multilingual Tasks**: Multi-Exam (M3Exam 5-shot, IndoMMLU 3-shot, ruMMLU 5-shot, mMMLU 5-shot), Multi-Understanding (BELEBELE 5-shot, XCOPA 5-shot, XWinograd 5-shot, XStoryCloze 0-shot, PAWS-X 5-shot), Multi-Mathematics (MGSM 8-shot), Multi-Translation (Flores-101 5-shot)
 

  
#### Qwen2-7B performance
|  Datasets  |  Mistral-7B  |   Gemma-7B |   Llama-3-8B  |   Qwen1.5-7B  |  Qwen2-7B  |
| :--------| :---------: | :------------: | :------------: | :------------: | :------------: |
|# Params | 7.2B | 8.5B | 8.0B | 7.7B | 7.6B  |
|# Non-emb Params | 7.0B | 7.8B | 7.0B | 6.5B | 6.5B |
|   ***English***  |    |    |   |    |	    |
|MMLU | 64.2 | 64.6 | 66.6 | 61.0 | **70.3** |
|MMLU-Pro | 30.9 | 33.7 | 35.4 | 29.9 | **40.0** |
|GPQA | 24.7 | 25.7 | 25.8 | 26.7 | **31.8** |
|Theorem QA | 19.2 | 21.5 | 22.1 | 14.2 | **31.1** |
|BBH  | 56.1 |  55.1  | 57.7 | 40.2 | **62.6** |
|HellaSwag  | **83.2** |  82.2  | 82.1 | 78.5 | 80.7 |
|Winogrande  | 78.4 |  **79.0**  | 77.4 |  71.3 |  77.0 |
|ARC-C  | 60.0 |  **61.1**  | 59.3 | 54.2 |  60.6 |
|TruthfulQA  | 42.2 |  44.8  | 44.0 | 51.1 |  **54.2** |
|   ***Coding***  |    |    |   |    |	    |
|HumanEval | 29.3 | 37.2 | 33.5 | 36.0 | **51.2**  |
|MBPP | 51.1 | 50.6 | 53.9 | 51.6 | **65.9**  |
|EvalPlus | 36.4 | 39.6 | 40.3 | 40.0 | **54.2**  |
|MultiPL-E | 29.4 | 29.7 | 22.6 | 28.1 | **46.3**  |
|   ***Mathematics***  |    |    |   |    |	    |
|GSM8K | 52.2 |  46.4  | 56.0 | 62.5 | **79.9** |
|MATH  | 13.1 |  24.3  | 20.5 | 20.3 | **44.2** |
|   ***Chinese***  |    |    |   |    |	    |
|C-Eval   | 47.4 |   43.6    |  49.5 |  74.1 |  **83.2** |
|CMMLU   | - |   -    | 50.8 | 73.1 | **83.9** |
|   ***Multilingual***  |    |    |   |    |	    |
|Multi-Exam   | 47.1 |   42.7    |  52.3 |  47.7 |  **59.2** |
|Multi-Understanding | 63.3 |  58.3    |  68.6 |  67.6 |  **72.0** |
|Multi-Mathematics | 26.3 |   39.1    |  36.3 |  37.3 |  **57.5** |
|Multi-Translation | 23.3 |   31.2    |  **31.9** |  28.4 |  31.5 |


## Citation

If you find our work helpful, feel free to give us a cite.

```
@article{qwen2,
  title={Qwen2 Technical Report},
  year={2024}
}
```