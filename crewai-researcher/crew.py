import sys
import json
import os
from crewai import Agent, Task, Crew, Process
from crewai_tools import SerperDevTool
from langchain_cohere import ChatCohere

# --- Input Topic ---
topic = sys.argv[1]

# --- Environment Variables ---
os.environ["COHERE_API_KEY"] = os.environ.get("COHERE_API_KEY")
os.environ["SERPER_API_KEY"] = os.environ.get("SERPER_API_KEY")

# --- Initialize LLM with Cohere ---
llm = ChatCohere(
    model_name="command-xlarge-nightly",
    cohere_api_key=os.environ.get("COHERE_API_KEY"),
    provider="cohere"  # important for LiteLLM/CrewAI
)

# --- Initialize Serper Tool ---
serper_tool = SerperDevTool(api_key=os.environ.get("SERPER_API_KEY"))

# --- Agents ---
researcher = Agent(
    role="Market Research Analyst",
    goal=f"Find reliable and recent data on '{topic}'.",
    backstory=(
        "You are a precise researcher who uses credible online sources to find "
        "statistics, studies, and factual information. You summarize key findings "
        "for writers to use in content creation."
    ),
    tools=[serper_tool],
    llm=llm,
    max_rpm=2,
    verbose=True,
    allow_delegation=False
)

writer = Agent(
    role="Creative Content Writer",
    goal=f"Write a short, engaging, and informative blog post about '{topic}'.",
    backstory=(
        "You're a skilled content writer who blends storytelling with factual accuracy. "
        "You can take raw data and turn it into something interesting and relatable."
    ),
    llm=llm,
    max_rpm=2,
    verbose=True,
    allow_delegation=False
)

editor = Agent(
    role="Senior Editor",
    goal="Polish the writing for clarity, grammar, and tone.",
    backstory=(
        "You're an experienced editor with an eye for flow and readability. "
        "You ensure the content feels polished, natural, and professional."
    ),
    llm=llm,
    max_rpm=2,
    verbose=True,
    allow_delegation=False
)

# --- Tasks ---
research_task = Task(
    description=(
        f"Use the Serper tool to research the topic '{topic}'. "
        "Your only valid tool input is a string, e.g., {'search_query': 'AI jobs future 2025'}. "
        "Gather statistics, trends, and facts from credible sources and summarize them "
        "in clear bullet points."
    ),
    expected_output="A bullet-point summary of factual findings about the topic.",
    agent=researcher
)

write_task = Task(
    description=(
        f"Using the research summary, write a 250-word blog post about '{topic}'. "
        "Keep it creative, balanced, and easy to read."
    ),
    expected_output="A well-written 250-word blog post draft.",
    agent=writer,
    context=[research_task]
)

edit_task = Task(
    description=(
        "Refine the blog draft for clarity, grammar, and flow. "
        "Ensure the tone is engaging and professional. Output the final version in Markdown."
    ),
    expected_output="The final blog post in polished Markdown format.",
    agent=editor,
    context=[write_task]
)

# --- Crew ---
crew = Crew(
    agents=[researcher, writer, editor],
    tasks=[research_task, write_task, edit_task],
    process=Process.sequential,
    verbose=True
)

# --- Execution ---
result = crew.kickoff()

# --- Safe Serialization ---
def safe_serialize(obj):
    if hasattr(obj, "model_dump"):
        return obj.model_dump()
    elif hasattr(obj, "__dict__"):
        return obj.__dict__
    elif isinstance(obj, (list, dict, str, int, float, bool)) or obj is None:
        return obj
    else:
        return str(obj)

final_output = {
    "final_result": safe_serialize(result.raw),
    "token_usage": safe_serialize(result.token_usage)
}

# --- Pretty Print ---
print(json.dumps(final_output, indent=2, default=safe_serialize))
