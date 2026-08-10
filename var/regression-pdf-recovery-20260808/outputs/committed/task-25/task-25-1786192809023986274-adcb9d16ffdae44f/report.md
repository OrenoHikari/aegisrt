# Research Goal

调研 2023 至 2026 年视觉语言模型在文档理解中的轻量化方法，比较代表性模型、数据集、评估指标和主要局限，并设计一个可以在单张消费级 GPU 上执行的后续实验。

# Search Strategy

- `lightweight vision language model document understanding`

# Papers Reviewed

- [P1] **Granite Vision: a lightweight, open-source multimodal model for enterprise Intelligence** (2025), Granite Vision Team, Leonid Karlinsky, Assaf Arbelle, Abraham Daniels, Ahmed Nassar, Amit Alfassi, Bo Wu, Eli Schwartz, Dhiraj Joshi, Jovana Kondic, Nimrod Shabtay, Pengyuan Li, Roei Herzig, Shafiq Abedin, Shaked Perek, Sivan Harary, Udi Barzelay, Adi Raz Goldfarb, Aude Oliva, Ben Wieles, Bishwaranjan Bhattacharjee, Brandon Huang, Christoph Auer, Dan Gutfreund, David Beymer, David Wood, Hilde Kuehne, Jacob Hansen, Joseph Shtok, Ken Wong, Luis Angel Bathen, Mayank Mishra, Maksym Lysak, Michele Dolfi, Mikhail Yurochkin, Nikolaos Livathinos, Nimrod Harel, Ophir Azulai, Oshri Naparstek, Rafael Teixeira de Lima, Rameswar Panda, Sivan Doveh, Shubham Gupta, Subhro Das, Syed Zawad, Yusik Kim, Zexue He, Alexander Brooks, Gabe Goodhart, Anita Govindjee, Derek Leist, Ibrahim Ibrahim, Aya Soffer, David Cox, Kate Soule, Luis Lastras, Nirmit Desai, Shila Ofek-koifman, Sriram Raghavan, Tanveer Syeda-Mahmood, Peter Staar, Tal Drory, Rogerio Feris
- [P2] **Index-Preserving Lightweight Token Pruning for Efficient Document Understanding in Vision-Language Models** (2025), Jaemin Son, Sujin Choi, Inyong Yun
- [P3] **VARCO-VISION-2.0 Technical Report** (2025), Young-rok Cha, Jeongho Ju, SunYoung Park, Jong-Hyeon Lee, Younghyun Yu, Youngjune Kim
- [P4] **Interpret, prune and distill Donut : towards lightweight VLMs for VQA on document** (2025), Adnan Ben Mansour, Ayoub Karine, David Naccache

# Main Research Directions

- Granite Vision extends the Granite family of LLMs trained on more than 12 trillion tokens.
- The model is trained with a four-stage curriculum with memory-efficient techniques.
- The proposed method is a lightweight pre-encoder token pruning framework that removes non-informative background patches using a binary text-region classifier with a max-pooling refinement step.

# Method Comparison

| Paper | Method | Datasets | Metrics |
|---|---|---|---|
| [P2] | The proposed method is a lightweight pre-encoder token pruning framework that removes non-informative background patches using a binary text-region classifier with a max-pooling refinement step. |  |  |
| [P1] | Granite Vision extends the Granite family of LLMs trained on more than 12 trillion tokens. | Document understanding data covers general document images, and more, charts, diagrams, encompassing diverse visual Q&A tasks., flowcharts |  |
| [P4] |  |  |  |
| [P3] | The model is trained with a four-stage curriculum with memory-efficient techniques. |  |  |

# Evidence-backed Findings

- **FACT:** The proposed method is a lightweight pre-encoder token pruning framework that removes non-informative background patches using a binary text-region classifier with a max-pooling refinement step. [P2]
- **FACT:** The framework preserves token indices to maintain the spatial correspondence required for layout-sensitive recognition. [P2]
- **FACT:** The proposed method discards background patches at the earliest stage, before any computation occurs in a VLM. [P2]
- **FACT:** ToMe showed low ANLS and accuracy because shuffling and rearranging tokens collapses the index structure, leading to poor text recognition. [P2]
- **FACT:** DocKylin with DTS showed limited performance because its merging strategy treats tokens highly correlated with others as background, which fails when the assumption of similar background patterns does not hold. [P2]
- **FACT:** Two images in the CORD subset were fully pruned and excluded from FLOPs computation, assigned 0 score for F1 and normalized TED accuracy. [P2]
- **FACT:** Existing models are predominantly trained on natural images, limiting performance in visual document understanding. [P1]
- **FACT:** Granite Vision is a compact vision-language model with approximately 3 billion parameters, tailored to excel in enterprise use cases. [P1]
- **FACT:** The first version of Granite Vision is particularly focused on visual document understanding. [P1]
- **FACT:** Granite Vision extends the Granite family of LLMs trained on more than 12 trillion tokens. [P1]
- **FACT:** A comprehensive instruction-following dataset for visual document understanding was curated, comprising around 13 million images and 80 million instructions. [P1]
- **FACT:** Training data is enriched by instruction-following data for general images from public datasets. [P1]
- **FACT:** The model demonstrates competitive results on standard vision-language benchmarks. [P1]
- **FACT:** The model is publicly available under the Apache-2 license, allowing visibility into training data and procedures. [P1]
- **FACT:** Granite Vision uniquely achieves state-of-the-art results on standard document and other benchmarks at a significantly reduced scale (~3B parameters). [P1]
- **FACT:** Document understanding data covers general document images, charts, flowcharts, diagrams, and more, encompassing diverse visual Q&A tasks. [P1]
- **FACT:** The highest scoring model on ChartQA is Granite Vision 3.1 with a score of 0.86. [P1]
- **FACT:** Donut-MINT achieves significant reductions in parameters and computation while closely matching teacher accuracy on DocVQA. [P4]
- **FACT:** Donut-MINT is a lightweight model that achieves competitive performance on DocVQA. [P4]
- **FACT:** The study centers on Donut as a proof of concept for a broader class of VLMs. [P4]
- **FACT:** Donut uses explicit cross-attention between visual features and decoder tokens, making modality fusion interpretable. [P4]
- **FACT:** Donut is trained to copy text spans from the image rather than generate free-form answers, making attribution of internal mechanisms more tractable. [P4]
- **FACT:** The model is trained with a four-stage curriculum with memory-efficient techniques. [P3]
- **FACT:** A 1.7B version optimized for on-device deployment is released. [P3]
- **FACT:** VARCO-VISION-2.0 is built on the LLaVA-OneVision architecture, combining a large language model, a vision encoder, and a two-layer MLP connector. [P3]
- **FACT:** The patch-16 setting reduces the number of visual tokens compared to patch-14. [P3]
- **FACT:** The training pipeline follows a four-stage curriculum designed to progressively build multimodal capability. [P3]
- **FACT:** The model is trained on approximately 6.5 billion text tokens and 30.4 billion image tokens. [P3]
- **INFERENCE:** The reviewed work separates into 3 distinct technical routes rather than one uniform method. [P1] [P2] [P3] [P4]
- **INFERENCE:** The reported numerical results are not directly comparable because the reviewed papers use different datasets or evaluation splits. [P1]

# Datasets and Metrics

- Datasets: Document understanding data covers general document images, and more, charts, diagrams, encompassing diverse visual Q&A tasks., flowcharts
- Metrics: 

# Current Limitations

- The study centers on Donut as a proof of concept for a broader class of VLMs.
- Two images in the CORD subset were fully pruned and excluded from FLOPs computation, assigned 0 score for F1 and normalized TED accuracy.

# Research Opportunities

- **INFERENCE:** The reviewed work separates into 3 distinct technical routes rather than one uniform method. [P1] [P2] [P3] [P4]
- **INFERENCE:** The reported numerical results are not directly comparable because the reviewed papers use different datasets or evaluation splits. [P1]

# Proposed Experiment

- **PROPOSAL — Hypothesis:** Combining complementary components from the reviewed routes "Granite Vision extends the Granite family of LLMs trained on more than 12 trillion tokens." and "The model is trained with a four-stage curriculum with memory-efficient techniques." will improve the primary evaluation metrics for the research goal "设计一个可以在单张消费级 GPU 上执行的后续实验，基于视觉语言模型在文档理解中的轻量化方法调研。" under a common protocol.

### Baselines

- **PROPOSAL:** Reproduce the The proposed method is a lightweight pre-encoder token pruning framework that removes non-informative background patches using a binary text-region classifier with a max-pooling refinement step. route from 2509.06415 as a separately reported baseline.
- **PROPOSAL:** Reproduce the Granite Vision extends the Granite family of LLMs trained on more than 12 trillion tokens. route from 2502.09927 as a separately reported baseline.
- **PROPOSAL:** Reproduce the  route from 2509.26235 as a separately reported baseline.
- **PROPOSAL:** Reproduce the The model is trained with a four-stage curriculum with memory-efficient techniques. route from 2509.10105 as a separately reported baseline.

### Datasets

- **PROPOSAL:** Evaluate on Document understanding data covers general document images with the original paper split and a documented common split where licensing permits.
- **PROPOSAL:** Evaluate on and more with the original paper split and a documented common split where licensing permits.
- **PROPOSAL:** Evaluate on charts with the original paper split and a documented common split where licensing permits.
- **PROPOSAL:** Evaluate on diagrams with the original paper split and a documented common split where licensing permits.
- **PROPOSAL:** Evaluate on encompassing diverse visual Q&A tasks. with the original paper split and a documented common split where licensing permits.
- **PROPOSAL:** Evaluate on flowcharts with the original paper split and a documented common split where licensing permits.

### Ablations

- **PROPOSAL:** Remove each proposed component one at a time while holding data, optimization, and evaluation settings fixed.
- **PROPOSAL:** Evaluate the two selected research-route components separately and then in combination.
- **PROPOSAL:** Repeat the comparison without auxiliary or cross-dataset pretraining to measure transfer dependence.

### Evaluation protocol

- **PROPOSAL:** Use identical train/validation/test identities for all baselines within each dataset.
- **PROPOSAL:** Report dataset-specific results separately before any macro average because published results are not directly comparable.

### Risks

- **PROPOSAL:** Dataset licensing or unavailable annotations may prevent an exact reproduction.
- **PROPOSAL:** The reviewed datasets and benchmark definitions may not represent deployment conditions outside the available evidence.
- **PROPOSAL:** Apply the stated experimental constraint: 单张消费级 GPU，如 NVIDIA RTX 3060 12GB，内存有限，需考虑模型大小和训练效率。

# Evidence / References

- [P1] Granite Vision Team, Leonid Karlinsky, Assaf Arbelle, Abraham Daniels, Ahmed Nassar, Amit Alfassi, Bo Wu, Eli Schwartz, Dhiraj Joshi, Jovana Kondic, Nimrod Shabtay, Pengyuan Li, Roei Herzig, Shafiq Abedin, Shaked Perek, Sivan Harary, Udi Barzelay, Adi Raz Goldfarb, Aude Oliva, Ben Wieles, Bishwaranjan Bhattacharjee, Brandon Huang, Christoph Auer, Dan Gutfreund, David Beymer, David Wood, Hilde Kuehne, Jacob Hansen, Joseph Shtok, Ken Wong, Luis Angel Bathen, Mayank Mishra, Maksym Lysak, Michele Dolfi, Mikhail Yurochkin, Nikolaos Livathinos, Nimrod Harel, Ophir Azulai, Oshri Naparstek, Rafael Teixeira de Lima, Rameswar Panda, Sivan Doveh, Shubham Gupta, Subhro Das, Syed Zawad, Yusik Kim, Zexue He, Alexander Brooks, Gabe Goodhart, Anita Govindjee, Derek Leist, Ibrahim Ibrahim, Aya Soffer, David Cox, Kate Soule, Luis Lastras, Nirmit Desai, Shila Ofek-koifman, Sriram Raghavan, Tanveer Syeda-Mahmood, Peter Staar, Tal Drory, Rogerio Feris. **Granite Vision: a lightweight, open-source multimodal model for enterprise Intelligence**. 2025. arxiv. `2502.09927`. https://arxiv.org/abs/2502.09927
- [P2] Jaemin Son, Sujin Choi, Inyong Yun. **Index-Preserving Lightweight Token Pruning for Efficient Document Understanding in Vision-Language Models**. 2025. arxiv. `2509.06415`. https://arxiv.org/abs/2509.06415
- [P3] Young-rok Cha, Jeongho Ju, SunYoung Park, Jong-Hyeon Lee, Younghyun Yu, Youngjune Kim. **VARCO-VISION-2.0 Technical Report**. 2025. arxiv. `2509.10105`. https://arxiv.org/abs/2509.10105
- [P4] Adnan Ben Mansour, Ayoub Karine, David Naccache. **Interpret, prune and distill Donut : towards lightweight VLMs for VQA on document**. 2025. arxiv. `2509.26235`. https://arxiv.org/abs/2509.26235

## Evidence Ledger

- `2509.06415-e2` [P2], section **ABSTRACT**, task `task-12`: We propose a lightweight pre-encoder token pruning framework that
removes non-informative background patches using a binary text-region classifier
with a max-pooling refinement step.
- `2509.06415-e3` [P2], section **ABSTRACT**, task `task-12`: The framework preserves token indices to
maintain the spatial correspondence required for layout-sensitive recognition.
- `2509.06415-e4` [P2], section **METHOD**, task `task-12`: We propose an index-preserving pruning method that discards background patches at the earliest
stage, before any computation occurs in a VLM.
- `2509.06415-e5` [P2], section **COMPARISON WITH EXISTING METHODS**, task `task-12`: ToMe (Bolya et al., 2022)
showed low ANLS and accuracy. As it shuffles
and rearranges tokens at each merge step, the
index structure collapses leading to poor text
recognition.
- `2509.06415-e6` [P2], section **COMPARISON WITH EXISTING METHODS**, task `task-12`: DocKylin (Zhang et al., 2025) with DTS showed limited performance. Its merging strategy treats
tokens highly correlated with others as background, under the assumption that backgrounds share
similar visual patterns. When this assumption does not hold, the method becomes less effective.
- `2509.06415-e7` [P2], section **EVALUATION SETUP**, task `task-12`: Two images in the CORD subset were fully pruned, meaning all patches were removed by the
classifier. We excluded them from the FLOPs computation and assigned 0 score for F1 and the
normalized TED accuracy.
- `2502.09927-e1` [P1], section **I NTRODUCTION**, task `task-13`: In addition, existing models are predominantly trained on natural
images, which can limit their performance in other domains, such as visual document understanding, where
the unique visual characteristics, such as layouts, fonts, and graphics, significantly differ from natural images
and require a more fine-grained comprehension of the visual content.
- `2502.09927-e2` [P1], section **I NTRODUCTION**, task `task-13`: In this work, we introduce Granite Vision, a compact vision-language model with approximately 3 billion
parameters1, tailored to excel in enterprise use cases.
- `2502.09927-e3` [P1], section **I NTRODUCTION**, task `task-13`: Although our model can process general images, the
first version of Granite Vision is particularly focused on visual document understanding, enabling automated
content extraction from tables, charts, infographics, plots, diagrams, sketches, and more.
- `2502.09927-e4` [P1], section **I NTRODUCTION**, task `task-13`: Granite Vision extends the Granite family of large language models ( Granite Team (2024)), which
have been trained on more than 12 trillion tokens, achieving state-of-the-art performance for their size, while
being designed for enterprise usage, with full visibility into the training data.
- `2502.09927-e6` [P1], section **Barque steak**, task `task-13`: As a key contribution of our work, we meticulously curate a comprehensive instruction-following dataset
for visual document understanding, comprising around 13 million images and 80 million instructions, which
span a diverse set of of tasks, including document question-answering , scene text understanding, key-value
extraction, text grounding, layout parsing, captioning, UI understanding, and code (see Figure 2).
- `2502.09927-e7` [P1], section **Barque steak**, task `task-13`: In addition to documents, our
training data is further enriched by the inclusion of instruction-following data for general images from public
datasets (Tong et al., 2024; Li et al., 2024b; Laurenc ¸on et al., 2024b; Liu et al., 2023b).
- `2502.09927-e8` [P1], section **Barque steak**, task `task-13`: In addition to its strong performance in enterprise settings, our model demonstrates competitive
results on standard vision-language benchmarks.
- `2502.09927-e9` [P1], section **Barque steak**, task `task-13`: To promote openness and collaboration, we make the model
publicly available under the Apache-2 license, allowing visibility into the training data and procedures.
- `2502.09927-e10` [P1], section **M ULTIMODAL LARGE LANGUAGE MODELS**, task `task-13`: Building on these insights, our model uniquely achieves state-of-the-art results on standard document and
other benchmarks, all while operating at a significantly reduced scale (around 3 billion parameters).
- `2502.09927-e11` [P1], section **D ATA**, task `task-13`: Our document understanding data (Figure 2) covers a
variety of document classes such as general document images, charts, flowcharts, diagrams and several more
encompassing a diverse set of visual Q&A tasks.
- `2502.09927-e12` [P1], section **Barque steak**, task `task-13`: What is the highest scoring model on
ChartQA and what is the score?
The highest scoring model on ChartQA is
Granite Vision 3.1 with a score of 0.86.
- `2509.26235-e2` [P4], section **Introduction**, task `task-16`: Our experiments show that our pruned model, Donut-MINT (Mechanistic
Interpretability-guided Network Trimming), achieves significant reductions in
both parameters and computation, while closely matching the teacher model’s
accuracy on DocVQA after distillation.
- `2509.26235-e3` [P4], section **Introduction**, task `task-16`: We introduce
Donut-MINT, a lightweight model that achieves competitive performance on
the DocVQA dataset [31], thereby validating our methodology and illustrating
how interpretability can drive principled model compression.
- `2509.26235-e4` [P4], section **Introduction**, task `task-16`: While
our study centers on Donut, we view it as a proof of concept for a broader class
of VLMs.
- `2509.26235-e5` [P4], section **Introduction**, task `task-16`: Second,
Donut uses explicit cross-attention between visual features and decoder tokens,
making its modality fusion interpretable and structurally disentangled.
- `2509.26235-e6` [P4], section **Introduction**, task `task-16`: Third,
it is trained to copy text spans from the image rather than generate free-form
answers, which makes attribution of internal mechanisms more tractable.
- `2509.10105-e1` [P3], section **Abstract**, task `task-22`: Trained
with a four-stage curriculum with memory-efficient techniques, the model achieves
enhanced multimodal alignment, while preserving core language abilities and
improving safety via preference optimization.
- `2509.10105-e2` [P3], section **Abstract**, task `task-22`: Alongside the 14B-scale model, we release
a 1.7B version optimized for on-device deployment.
- `2509.10105-e4` [P3], section **Architecture**, task `task-22`: VARCO-VISION-2.0 is built on the LLaV A-OneVision [2] architecture, combining a large language
model (LLM), a vision encoder, and a two-layer MLP connector that projects image features into the
LLM’s embedding space.
- `2509.10105-e6` [P3], section **Architecture**, task `task-22`: The patch-16 setting reduces the number
of visual tokens compared to patch-14; for example, a 384×384 input produces 242 = 576tokens
with patch-16, considerably fewer than the 272 = 729tokens produced by patch-14.
- `2509.10105-e7` [P3], section **Training Strategies and Datasets**, task `task-22`: Our training pipeline follows a four-stage curriculum [6, 7] designed to progressively build multimodal
capability.
- `2509.10105-e8` [P3], section **Training Strategies and Datasets**, task `task-22`: The model is trained on approximately 6.5 billion text tokens and 30.4 billion image tokens.
