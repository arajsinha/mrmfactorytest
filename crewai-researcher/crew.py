import sys
import json
import os
from crewai import Agent, Task, Crew, Process
from crewai_tools import SerperDevTool
from langchain_groq import ChatGroq
# Import from the modern pydantic library directly.
from pydantic import BaseModel, Field

# --- THIS IS THE FIX (Part 1) ---
# 2. Define a simple, strict input schema for our search tool.
#    This ensures the agent always provides a simple string.
class SearchInput(BaseModel):
    search_query: str = Field(description="Mandatory search query you want to use to search the internet")

# 3. Create a wrapper for the SerperDevTool
#    We are decorating the original tool to use our new, stricter input model.
search_tool = SerperDevTool(args_schema=SearchInput)
# --- END OF FIX ---


# Get the research topic from the command-line argument
topic = sys.argv[1]

os.environ["GROQ_API_KEY"] = os.environ.get("GROQ_API_KEY")
os.environ["SERPER_API_KEY"] = os.environ.get("SERPER_API_KEY")

llm = ChatGroq(model_name="groq/llama-3.1-8b-instant")

# Define the Researcher Agent
researcher = Agent(
  role='Senior Research Analyst',
  goal=f'Uncover groundbreaking technologies and trends about {topic}',
  backstory="You're a renowned research analyst.",
  tools=[search_tool],
  llm=llm,
  max_rpm=2,
  verbose=True,
  allow_delegation=False
)

# Define the Writer Agent
writer = Agent(
  role='Senior Technology Writer',
  goal=f'Craft a compelling and informative blog post about {topic}',
  backstory="You're a famous technology writer.",
  llm=llm,
  max_rpm=2,
  verbose=True,
  allow_delegation=False
)

# Define the Tasks
research_task = Task(
  description=f'Conduct a comprehensive analysis of the latest trends in {topic}. Identify key players, innovations, and market forecasts.',
  expected_output='A detailed report summarizing your findings.',
  agent=researcher,
  max_retries=3
)

write_task = Task(
  description='Using the research report, write an engaging blog post. It should be easy to understand, well-structured, and highlight the most important findings.',
  expected_output=f'A 500-word blog post about {topic}, formatted in markdown.',
  agent=writer,
  max_retries=3
)

crew = Crew(
  agents=[researcher, writer],
  tasks=[research_task, write_task],
  process=Process.sequential,
  verbose=True
)

# Execute the crew's work
result = crew.kickoff()

# Print the final result as a JSON object to standard output
print(json.dumps({"result": result}))