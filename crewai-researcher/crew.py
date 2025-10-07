import sys
import json
import os
from crewai import Agent, Task, Crew, Process
from crewai_tools import SerperDevTool
from langchain_cohere import ChatCohere
from typing import Type, Any
import requests

# --- The Custom Tool for State Management ---
# This tool is the agent's interface to our MCP Server and etcd backend.

class StorageToolInput(BaseModel):
    action: str = Field(description="The action to perform: 'save' or 'get'.")
    task_id: str = Field(description="The unique ID of the task to save or retrieve.")
    data: Any = Field(description="The content to save (e.g., research notes or a blog draft). Only used with the 'save' action.", default=None)

class EtcdStorageTool(BaseTool):
    name: str = "Persistent Task Storage"
    description: str = "A tool to save the output of a completed task to shared storage or get the output of a previous task."
    args_schema: Type[BaseModel] = StorageToolInput
    
    def _run(self, action: str, task_id: str, data: Any = None) -> str:
        # The internal Kubernetes DNS name for our MCP server
        mcp_server_url = "http://mcp-server.default.svc.cluster.local"
        
        if action == "save":
            try:
                # The agent saves its final output to the MCP server, which stores it in etcd.
                payload = {"id": task_id, "status": "completed", "output": data}
                response = requests.post(f"{mcp_server_url}/tasks/{task_id}", json=payload)
                response.raise_for_status() # Raise an exception for bad status codes
                return f"Successfully saved output for task {task_id}"
            except Exception as e:
                return f"Error saving task output: {e}"
        elif action == "get":
            try:
                # The agent retrieves the output of a dependency from the MCP server.
                response = requests.get(f"{mcp_server_url}/tasks/{task_id}")
                response.raise_for_status()
                task_data = response.json()
                return task_data.get("output", "No output found for this task.")
            except Exception as e:
                return f"Error getting task output: {e}"
        else:
            return "Invalid action. Use 'save' or 'get'."

# --- End of Custom Tool ---


storage_tool = EtcdStorageTool()

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
    tools=[serper_tool, storage_tool],
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
    tools=[storage_tool],
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
    tools=[storage_tool],
    llm=llm,
    max_rpm=2,
    verbose=True,
    allow_delegation=False
)

# --- Tasks ---
# Generate unique IDs for our stateful tasks
research_task_id = f"research-{uuid.uuid4()}"
writer_task_id = f"writer-draft-{uuid.uuid4()}"


# --- Tasks ---
research_task = Task(
    description=(
        f"Use the Serper tool to research the topic '{topic}'. "
        "Your only valid tool input is a string, e.g., {'search_query': 'AI jobs future 2025'}. "
        "Gather statistics, trends, and facts from credible sources and summarize them "
        "in clear bullet points."
        f"After gathering all facts, you MUST use the Persistent Task Storage tool to save your final summary. "
        f"Use the action 'save' and the task_id '{research_task_id}'."
    ),
    expected_output="A bullet-point summary of factual findings about the topic. Research summary was saved successfully.",
    agent=researcher
)

write_task = Task(
    description=(
        f"First, use the Persistent Task Storage tool to retrieve the research summary. "
        f"Use the action 'get' and the task_id '{research_task_id}'. "
        f"Then, write a 250-word blog post based on that summary. It should be creative, engaging, and informative and easy to understand. "
        f"After writing, you MUST use the Persistent Task Storage tool to save your final draft. "
        f"Use the action 'save' and the task_id '{writer_task_id}'."
    ),
    expected_output="A well-written 250-word blog post draft. Blog draft was saved successfully.",
    agent=writer,
)

edit_task = Task(
    description=(
        f"First, use the Persistent Task Storage tool to retrieve the blog post draft. "
        f"Use the action 'get' and the task_id '{writer_task_id}'. "
        f"Then, review the draft, correct any errors, improve the style, and output the final, polished version."
    ),
    expected_output="The final blog post in polished Markdown format.",
    agent=editor,
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
